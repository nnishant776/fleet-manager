package flows

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
)

// A host's first switch to a fleet configuration is a trial: the new system
// is activated with a timer armed that brings back the one before, and only
// once fleetmgr has logged in to it as support is the timer stopped and the
// new system made the boot default. A configuration that cuts the host off,
// its network or its SSH, so undoes itself. The steps take the target from
// the values chooseSwitch leaves: switch_ref, switch_env and eval_flake.

//go:embed trial-switch.sh
var trialSwitchScript string

// trialMinutesInput is how long the trial lasts.
var trialMinutesInput = engine.Input{
	Name: "trial-minutes", Prompt: "Minutes the host has to prove its new system",
	Help:    "After the switch, fleetmgr must log in to the new system as support within this time, or the host goes back to the system it ran before; 5 by default",
	Pattern: `^[1-9][0-9]?$`,
	Default: func(*engine.Ctx) (string, error) { return "5", nil },
}

// storePath is a system's path as the host may print it.
var storePath = regexp.MustCompile(`^/nix/store/[0-9a-df-np-sv-z]{32}-[A-Za-z0-9+._?=-]+$`)

// trialSwitchSteps switch the host to its new configuration, for the steps
// when says should. afterBuild come between the build and the trial, when the
// new system is known and nothing has changed yet.
func trialSwitchSteps(when func(*engine.Ctx) (bool, string), afterBuild ...engine.Step) []engine.Step {
	for i := range afterBuild {
		afterBuild[i].When = when
	}
	steps := []engine.Step{
		{
			Title: "Fetch {{.host}}'s secrets before the switch",
			Why: "The new system takes its secrets, support's keys among them, from the host's snapshot of the secrets repository, which only its sync makes. " +
				"So the host fetches, verifies and decrypts them now, as its sync will, with the signers file of the configuration it is switched to.",
			When: when,
			Do:   engine.Func(fetchSecretsFirst),
		},
		{
			Title: "Build {{.host}}'s new system",
			Why:   "On the host, as a unit, so that a dropped connection does not stop it. Nothing changes on the host yet.",
			When:  when,
			Do:    engine.Func(buildNewSystem),
		},
	}
	steps = append(steps, afterBuild...)
	return append(steps, []engine.Step{
		{
			Title: "Try the new system on {{.host}}, with a way back",
			Why: "A timer is armed first: unless fleetmgr confirms the new system within {{.trial-minutes}} minutes, the host goes back to the one it runs now. " +
				"Then the new system is activated, without becoming the boot default, so a reboot goes back as well.",
			When: when,
			Do:   engine.Func(tryNewSystem),
		},
		{
			Title: "Confirm {{.host}} works on its new system",
			Why:   "As {{.user}}, with nothing forwarded, as every step after this one logs in: if it can, the timer is stopped and the new system stays.",
			When:  when,
			Do:    engine.Func(confirmNewSystem),
		},
		{
			Title: "Make the new system {{.host}}'s boot default",
			Why:   "It becomes the system profile's newest generation and the boot loader's default, as a plain switch would have made it.",
			When:  when,
			Do:    engine.Func(makeBootDefault),
		},
	}...)
}

// secretsTool is the command that runs the secrets sync on the host: its own,
// or, on a host without fleetmgr's tools yet, the one of the fleet-manager
// commit checked for the switch.
func secretsTool(x *engine.Ctx) (string, error) {
	if x.Value("has_tooling") == "true" {
		return "fleetmgr-secrets-sync", nil
	}
	if x.Value("installer_rev") == "" {
		rev, err := signedUpstreamMain(x)
		if err != nil {
			return "", err
		}
		x.Values["installer_rev"] = rev
	}
	return "nix --extra-experimental-features 'nix-command flakes' run " + engine.Quote(installerAttr(x, "fleetmgr-secrets-sync")) + " --", nil
}

// evalTarget is the attribute path of the host's configuration in the flake
// it is switched to.
func evalTarget(x *engine.Ctx) (string, error) {
	flake, err := x.Expand(x.Value("eval_flake"))
	if err != nil {
		return "", err
	}
	if flake == "" {
		flake = x.Value("flake")
	}
	return flake + "#nixosConfigurations." + x.Value("host") + ".config.", nil
}

func fetchSecretsFirst(x *engine.Ctx) error {
	attr, err := evalTarget(x)
	if err != nil {
		return err
	}
	read := func(opt, apply string) (string, error) {
		args := []string{"eval", "--raw", attr + opt}
		if apply != "" {
			args = append(args, "--apply", apply)
		}
		res, err := engine.Cmd("nix", args...).ReadOnly().Quiet().Run(x)
		if err != nil {
			return "", fmt.Errorf("evaluating %s: %w: %s", opt, err, strings.TrimSpace(res.Stderr))
		}
		return res.Stdout, nil
	}
	url, err := read("conf.secrets.repo.url", "")
	if err != nil {
		return err
	}
	ref, err := read("conf.secrets.repo.ref", "")
	if err != nil {
		return err
	}
	state, err := read("conf.secrets.statePath", "")
	if err != nil {
		return err
	}
	signers, err := read("conf.fleet.signing.allowedSignersFile", `f: if f == null then "" else builtins.readFile f`)
	if err != nil {
		return err
	}
	if !x.Options.DryRun && (strings.TrimSpace(url) == "" || strings.TrimSpace(signers) == "") {
		return fmt.Errorf("the configuration %s is switched to sets no secrets repository, or no signers file: its sync could take no secrets", x.Value("host"))
	}
	tool, err := secretsTool(x)
	if err != nil {
		return err
	}
	x.Values["first_signers"] = signers
	x.Values["first_state"] = engine.Quote(strings.TrimSpace(state))
	x.Values["first_sync"] = tool + " sync --state " + engine.Quote(strings.TrimSpace(state)) + " --host {{.host}} --repo " +
		engine.Quote(strings.TrimSpace(url)) + " --ref " + engine.Quote(strings.TrimSpace(ref)) +
		" --signers " + engine.Quote(strings.TrimSpace(state)+"/allowed_signers.first")
	sync, err := x.Expand(x.Value("first_sync"))
	if err != nil {
		return err
	}
	x.Values["first_sync"] = sync
	if _, err := fleetmgr.OnAdmin(`sudo -n install -d -m 0700 {{.first_state}} && sudo -n sh -c "umask 022 && cat > {{.first_state}}/allowed_signers.first"`).
		StdinFrom("first_signers").StdinNote("the configuration's signers file").OK("wrote the signers file the sync checks commits with").Run(x); err != nil {
		return err
	}
	_, err = fleetmgr.OnAdmin("sudo -n {{.first_sync}}").OK("fetched, verified and decrypted").Run(x)
	return err
}

func buildNewSystem(x *engine.Ctx) error {
	x.Values["build_target"] = engine.Quote(x.Value("switch_ref") + "#nixosConfigurations." + x.Value("host") + ".config.system.build.toplevel")
	x.Values["switch_env"] = ""
	if sshURL(x.Value("config_url")) && strings.HasPrefix(x.Value("switch_ref"), "git+ssh://") {
		// A private configuration: nix fetches it with the host's own key.
		if err := writePinnedKeys(x); err != nil {
			return err
		}
		x.Values["switch_env"] = engine.Quote("--setenv=GIT_SSH_COMMAND=" + x.Value("ssh_private"))
	}
	res, err := fleetmgr.StillRunning(fleetmgr.OnAdmin(`sudo -n systemctl reset-failed fleetmgr-trial-build 2>/dev/null; `+
		`sudo -n install -d -m 0755 /run/fleetmgr-trial && `+
		`sudo -n systemd-run --wait --collect --unit=fleetmgr-trial-build `+
		`--setenv=PATH=/run/wrappers/bin:/run/current-system/sw/bin {{.switch_env}} `+
		`sh -c 'nix --extra-experimental-features "nix-command flakes" build --no-link --print-out-paths "$1" > /run/fleetmgr-trial/system' sh {{.build_target}}; s=$?; `+
		`sudo -n journalctl -u fleetmgr-trial-build -o cat -n 15 --no-pager; `+
		`[ $s = 0 ] && echo "system=$(cat /run/fleetmgr-trial/system)"; exit $s`).OK("built"),
		"the build", "fleetmgr-trial-build", "{{.admin}}").Run(x)
	if err != nil || x.Options.DryRun {
		x.Values["new_system"] = "<new system>"
		return err
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "system="); ok {
			x.Values["new_system"] = v
		}
	}
	if !storePath.MatchString(x.Value("new_system")) {
		return fmt.Errorf("the build on %s gave no system's store path", x.Value("host"))
	}
	x.UI.OK("the new system is " + x.Value("new_system"))
	return nil
}

func tryNewSystem(x *engine.Ctx) error {
	x.Values["trial_script"] = trialSwitchScript
	if _, err := fleetmgr.OnAdmin("sudo -n install -d -m 0755 /run/fleetmgr-trial && sudo -n sh -c 'umask 077 && cat > /run/fleetmgr-trial/trial-switch.sh'").
		StdinFrom("trial_script").StdinNote("trial-switch.sh").Run(x); err != nil {
		return err
	}
	cmd := fleetmgr.OnAdmin("sudo -n sh /run/fleetmgr-trial/trial-switch.sh {{.new_system}} {{.trial-minutes}}").OK("activated for a trial").
		Before(func(x *engine.Ctx) error {
			x.Running("trial", "{{.host}} runs its new system for a trial: unless it is confirmed, it goes back to the one before by itself within {{.trial-minutes}} minutes")
			return nil
		})
	res, err := cmd.Run(x)
	if err != nil && res.Code == 255 {
		// The activation may well restart the network or sshd under the
		// connection: the next step finds out whether the new system answers.
		x.UI.Note("The connection to " + x.Value("host") + " dropped during the activation, which a new network or SSH configuration can do. The next step tries to reach the new system.")
		return nil
	}
	return err
}

// confirmTries and confirmPause are how long confirmNewSystem keeps trying to
// log in: about a minute and a half. A test makes them short.
var (
	confirmTries = 18
	confirmPause = 5 * time.Second
)

func confirmNewSystem(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing was activated, so there is nothing to confirm")
		return nil
	}
	script := `cur=$(readlink -f /run/current-system); if [ "$cur" != {{.new_system}} ]; then echo "running=$cur"; exit 3; fi; ` +
		`sudo -n systemctl stop fleetmgr-trial-rollback.timer && echo confirmed`
	var last error
	for i := 0; i < confirmTries; i++ {
		if i > 0 {
			select {
			case <-x.Context.Done():
				return x.Context.Err()
			case <-time.After(confirmPause):
			}
		}
		cmd := fleetmgr.OnHost(script).ReadOnly()
		var res engine.Result
		var err error
		if i == 0 {
			res, err = cmd.Quiet().OK("{{.user}} logged in to the new system").Run(x)
		} else {
			res, err = cmd.RunQuiet(x)
		}
		switch {
		case err == nil && strings.Contains(res.Stdout, "confirmed"):
			x.Running("trial", "")
			if i > 0 {
				x.UI.OK("{{.user}} logged in to the new system")
			}
			x.UI.OK("stopped the timer: the new system stays")
			return nil
		case strings.Contains(res.Stdout, "running="):
			x.Running("trial", "")
			return fmt.Errorf("%s does not run the new system any more: it has gone back already, so the new system cut it off or the trial ran out", x.Value("host"))
		}
		last = err
		if i == 0 {
			x.UI.Skip("not reachable as {{.user}} yet; trying again for about a minute")
		}
	}
	return fmt.Errorf("could not log in to %s's new system as %s (%v). It goes back to the system it ran before by itself, "+
		"within %s minutes of the switch: look at what the new configuration does to its network and SSH, then run this again",
		x.Value("host"), x.Value("user"), last, x.Value("trial-minutes"))
}

func makeBootDefault(x *engine.Ctx) error {
	_, err := fleetmgr.OnHost("sudo -n nix-env -p /nix/var/nix/profiles/system --set {{.new_system}} && " +
		"sudo -n {{.new_system}}/bin/switch-to-configuration boot").OK("the new system is the boot default").Run(x)
	if err == nil {
		x.Values["switched"] = "true"
	}
	return err
}

// joinSteps makes one list of a flow's steps from its parts.
func joinSteps(parts ...[]engine.Step) []engine.Step {
	var all []engine.Step
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}
