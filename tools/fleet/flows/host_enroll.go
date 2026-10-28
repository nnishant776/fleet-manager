package flows

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// HostEnroll brings a host that is already running into the secrets setup:
// what was done by hand for neerajpinew on 2026-10-18. Every step looks first
// and skips what is already done, so it also finishes a half-done enrollment.
var HostEnroll = &engine.Flow{
	Path:  []string{"host", "enroll"},
	Title: "Enroll a running host in the secrets setup",
	Short: "Enroll a running host: identity, enrollment, deploy key, first sync",
	Long: "For a host that already runs, and whose configuration in this repository sets\n" +
		"conf.secrets.repo.url. fleet logs in as its admin account through an agent that\n" +
		"holds one key only, switches the host to that configuration if it does not run\n" +
		"it yet or runs another repository's, creates its identity, copies its enrollment file here and checks it,\n" +
		"enrolls it in the secrets repository and pushes, registers its deploy key\n" +
		"read-only, then syncs the host and checks it. Steps that are done already are\n" +
		"skipped, so it can be run again after a failure.",
	Example: "  fleet host enroll neerajpinew --key ~/.ssh/home_network_ssh_key --repo ~/Projects/fleet-secrets",
	Args:    []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Which host?", Help: "A host of the configuration (hosts/<name>)",
			Pattern: secrets.NamePattern, Choices: fleet.Hosts},
		{Name: "address", Prompt: "Address to reach it at", Help: "A name or IP address for SSH; defaults to the host's name",
			Default: func(x *engine.Ctx) (string, error) { return x.Value("host"), nil }},
		{Name: "admin", Prompt: "Account with sudo by SSH key", Help: "The account to log in as until the host has support; admin by default",
			Default: func(*engine.Ctx) (string, error) { return "admin", nil }},
		{Name: "user", Prompt: "Support account", Help: "The account for the steps after the switch; support by default, which needs nothing forwarded",
			Default: func(*engine.Ctx) (string, error) { return "support", nil }},
		{Name: "key", Prompt: "Key that logs in to that account and unlocks its sudo",
			Help: "A private key file; the only key the agent fleet starts holds and forwards",
			Default: func(*engine.Ctx) (string, error) {
				home, _ := os.UserHomeDir()
				if p := filepath.Join(home, ".ssh", "id_ed25519"); fileExists(p) {
					return p, nil
				}
				return "", nil
			}},
		secrets.RepoInput,
		secrets.RecoveryInput,
		{Name: "switch-to", Prompt: "Configuration to switch the host to, if it needs a switch",
			Help:     "A flake reference; defaults to the newest commit of " + fleet.Upstream + "'s main, once it is signed by a key in the checkout's fleet/allowed_signers",
			Optional: true, Default: func(*engine.Ctx) (string, error) { return "", nil }},
		{Name: "register-with", Prompt: "Command that registers the deploy key",
			Help:     "Run with the public key on stdin, instead of gh or the web page; mostly for tests",
			Optional: true, Default: func(*engine.Ctx) (string, error) { return "", nil }},
		{Name: "sync", Prompt: "Sync the host at the end and check it?",
			Help: "Sync the host at the end and run fleet host check (default true)", Bool: true,
			Default: func(*engine.Ctx) (string, error) { return "true", nil }},
	},
	Needs:   []string{"ssh", "ssh-agent", "ssh-add", "git", "yq", "sops", "age"},
	Cleanup: fleet.StopAgent,
	Steps: []engine.Step{
		{
			Title: "Start an agent that holds one key",
			Why:   "{{.admin}}'s sudo checks its key through the forwarded agent. Only this agent is forwarded, never yours, which holds your signing key.",
			Do:    engine.Func(fleet.StartAgent),
		},
		{
			Title: "Look at {{.host}}",
			Why:   "What is done already decides which of the next steps run.",
			Do:    engine.Func(lookAtHost),
		},
		{
			Title: "Choose the commit to switch {{.host}} to",
			Why: "It does not run a configuration with secrets from this repository yet. Unless --switch-to names one, it gets a commit a key you trust has signed, as its own sync will take no other: " +
				"for a private configuration the one fleet install prep would install, otherwise the newest commit of main.",
			When: needsSwitch,
			Do:   engine.Func(chooseSwitch),
		},
		{
			Title: "Create {{.host}}'s identity",
			Why: "Its age key decrypts its secrets, its deploy key fetches them, and its configuration key fetches a private configuration. All are made on the host and never leave it; init makes only what is missing. " +
				"A host without fleet's tools yet runs init from the fleet-manager commit checked above, before its switch, which for a private configuration needs the key.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("has_identity") == "true" && x.Value("has_config_key") == "true" {
					return false, "it has one"
				}
				return true, ""
			},
			Do: engine.Func(createIdentity),
		},
		{
			Title: "Copy its enrollment file here",
			Why:   "It holds the public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.",
			Do:    engine.Func(copyEnrollment),
		},
		{
			Title: "Enroll {{.host}} in the secrets repository",
			Why:   "fleet secrets host-enroll creates its branch, host/{{.host}}, with its empty file encrypted to it and to your recovery recipient, each step shown.",
			When:  enrolledWithThisIdentity,
			Do:    engine.Func(runHostEnroll),
		},
		{
			Title: "Push host/{{.host}}",
			Why:   "{{.host}} fetches its branch from GitHub; it accepts the commit only if it is signed by an allowed key.",
			When:  aheadOfUpstream,
			Do:    secrets.PushBranch(),
		},
		{
			Title: "Register {{.host}}'s deploy key, read-only",
			Why:   "It is how {{.host}} fetches the private secrets repository. Read-only: a host never writes to it.",
			Do:    engine.Func(registerDeployKey),
		},
		{
			Title: "Register {{.host}}'s configuration key, read-only",
			Why:   "Its configuration comes over SSH, from a private repository, and this key is how {{.host}} fetches it. Read-only, like the deploy key.",
			When: func(x *engine.Ctx) (bool, string) {
				if !sshURL(x.Value("config_url")) {
					return false, "its configuration repository needs no key: " + x.Value("config_url") + " is not an SSH address"
				}
				return true, ""
			},
			Do: engine.Func(registerConfigKey),
		},
		{
			Title: "Check its configuration fits {{.host}} as it is",
			Why: "Without a reinstall, the configuration must describe the disks the machine has and boot the way it boots: " +
				"a root or boot filesystem it does not have, or a UEFI boot loader on a machine without UEFI, would leave it unbootable after the switch.",
			When: needsSwitch,
			Do:   engine.Func(checkFits),
		},
		{
			Title: "Check host/{{.host}} holds the secrets the configuration declares",
			Why: "After the switch the host's sync refuses a snapshot that lacks a secret the configuration declares, such as the keys of an account that takes them from its secrets. " +
				"The branch is read as it is in git, without decrypting; if something is missing, this stops before the switch with the command that adds it.",
			When: needsSwitch,
			Do:   engine.Func(checkBranchHoldsSecrets),
		},
		{
			Title: "Warn that /etc/nixos on {{.host}} will be replaced",
			Why: "/etc/nixos belongs to the fleet's configuration: nothing it holds now is kept. This step changes nothing; it shows what is there and asks, before the switch, " +
				"so that you can copy what you need first.",
			When: warnsAboutEtcNixos,
			Do:   engine.Func(warnAboutEtcNixos),
		},
		{
			Title: "Switch {{.host}} to its fleet configuration",
			Why: "The switch runs as a service on the host, so it finishes even if this connection drops. A private configuration is fetched with the host's configuration key. " +
				"/etc/nixos is replaced after it.",
			When: needsSwitch,
			Do:   engine.Func(switchHost),
		},
		{
			Title: "Make /etc/nixos the configuration repository's checkout",
			Why: "The sync owns /etc/nixos and fetches the repository the configuration names. What was there is deleted, and the first-boot clone, fleet-bootstrap, " +
				"makes the checkout at the commit the host runs and records it as the first the machine accepted.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("switched") == "true" {
					return true, ""
				}
				if ok, _ := needsSwitch(x); ok && x.Options.DryRun {
					return true, ""
				}
				return false, "this run did not switch the host; its sync owns /etc/nixos"
			},
			Do: engine.Func(remakeEtcNixos),
		},
		{
			Title: "Sync {{.host}}",
			Why:   "Its first sync with secrets: fetch and verify them, then its configuration.",
			When:  wantSync,
			Do:    fleet.StillRunning(fleet.OnHost("sudo -n systemctl start config-auto-update"), "the sync", "config-auto-update", "{{.user}}"),
		},
		{
			Title: "Check {{.host}}",
			Why:   "fleet host check, as support with nothing forwarded: this also shows support works.",
			When:  wantSync,
			Do: engine.Cmd("fleet", "host", "check", "{{.host}}", "--address", "{{.address}}", "--flake", "{{.flake}}").
				ReadOnly().Interactive(),
		},
	},
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// unless skips a step when a fact from lookAtHost is true.
func unless(fact, reason string) func(*engine.Ctx) (bool, string) {
	return func(x *engine.Ctx) (bool, string) {
		if x.Value(fact) == "true" {
			return false, reason
		}
		return true, ""
	}
}

// enrollFacts are the values lookAtHost takes from the host's answer. Anything
// else the host prints is ignored: a host it cannot trust yet must not be able
// to set the address, the flake or the command that registers its key.
var enrollFacts = map[string]bool{
	"login": true, "sudo": true, "has_tooling": true, "has_identity": true,
	"has_config_key": true, "has_checkout": true, "origin": true, "has_support": true,
	"etc_nixos": true, "dirty": true,
}

// lookAtHost records what is done already as values: has_tooling,
// has_identity, has_checkout, origin, has_support, config_url; and what
// /etc/nixos holds: etc_nixos is git, files or empty, and dirty says whether
// a checkout there has changes of its own.
func lookAtHost(x *engine.Ctx) error {
	res, err := fleet.OnAdmin(`set -u
echo "login=$(id -un)"
sudo -n true 2>/dev/null && echo sudo=true || echo sudo=false
command -v fleet-secrets-sync >/dev/null && echo has_tooling=true || echo has_tooling=false
sudo -n test -f /var/lib/fleet/age.key && echo has_identity=true || echo has_identity=false
sudo -n test -f /var/lib/fleet/config_deploy_key && echo has_config_key=true || echo has_config_key=false
test -d /etc/nixos/.git && echo has_checkout=true || echo has_checkout=false
g() { sudo -n git -c safe.directory=/etc/nixos -C /etc/nixos "$@"; }
echo "origin=$(g remote get-url origin 2>/dev/null)"
if sudo -n test -d /etc/nixos/.git; then echo etc_nixos=git
elif [ -n "$(sudo -n ls -A /etc/nixos 2>/dev/null)" ]; then echo etc_nixos=files
else echo etc_nixos=empty; fi
if sudo -n test -d /etc/nixos/.git && [ -n "$(g status --porcelain 2>/dev/null)" ]; then echo dirty=true; else echo dirty=false; fi
id support >/dev/null 2>&1 && echo has_support=true || echo has_support=false`).RunQuiet(x)
	if err != nil {
		return fmt.Errorf("could not log in as %s: %w", x.Value("admin"), err)
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && enrollFacts[k] {
			x.Values[k] = v
		}
	}
	if x.Value("sudo") != "true" {
		return fmt.Errorf("%s has no sudo with the key in the agent: is %s one of its sudoSshKeys?", x.Value("admin"), x.Value("key"))
	}
	report := func(fact, yes, no string) {
		if x.Value(fact) == "true" {
			x.UI.OK(yes)
		} else {
			x.UI.Skip(no)
		}
	}
	x.UI.OK("logged in as " + x.Value("login") + ", with sudo")
	report("has_tooling", "runs a configuration with secrets", "does not run a configuration with secrets yet")
	report("has_identity", "has its identity", "has no identity yet")
	report("has_checkout", "has a checkout in /etc/nixos, fetching "+x.Value("origin"), "has no checkout in /etc/nixos")
	report("has_support", "has the support account", "has no support account yet")
	// Where the configuration says the host syncs from, if the flake can tell.
	if url, err := engine.Silently(x, nil, "nix", "eval", "--raw", x.Value("flake")+"#nixosConfigurations."+x.Value("host")+".config.conf.fleet.repo.url"); err == nil {
		x.Values["config_url"] = url
	}
	return nil
}

// chooseSwitch settles what the host switches to, as switch_ref, and as
// switch_rev the commit when it is main's, checked here.
func chooseSwitch(x *engine.Ctx) error {
	ref := x.Value("switch-to")
	evalFlake := "{{.switch_ref}}"
	switch {
	case ref != "":
	case sshURL(x.Value("config_url")):
		// A private configuration: the commit fleet install prep would
		// install, on the branch the host tracks, and the fleet-manager commit
		// its flake.lock pins, both checked against your copies of the keys.
		// prep's checks read the checkout's own origin; the host's is kept.
		hostOrigin := x.Value("origin")
		err := checkConfigCommit(x)
		if err == nil {
			err = checkInstallerCommit(x)
		}
		x.Values["origin"] = hostOrigin
		if err != nil {
			return err
		}
		ref = flakeAt(x.Value("config_url"), x.Value("config_ref"), x.Value("inv"))
		x.Values["switch_rev"], x.Values["installer_rev"] = x.Value("inv"), x.Value("fm")
		// Read from the checkout here, the same files at that commit, rather
		// than fetched over SSH.
		evalFlake = "{{.flake}}"
	default:
		rev, err := signedUpstreamMain(x)
		if err != nil {
			return err
		}
		// Fetched with git, so what the host builds is that commit's tree: a
		// github: tarball is not checked against the commit it is named for.
		ref = "git+" + fleet.UpstreamURL + "?rev=" + rev
		x.Values["switch_rev"], x.Values["installer_rev"] = rev, rev
	}
	x.Values["switch_ref"] = ref
	x.Values["eval_flake"] = evalFlake
	// For the host's shell: a git+https reference has a ? in it.
	x.Values["switch_target"] = engine.Quote(ref + "#" + x.Value("host"))
	res, err := engine.Cmd("nix", "eval", "--raw", evalFlake+"#nixosConfigurations.{{.host}}.config.conf.secrets.repo.url").
		ReadOnly().Quiet().OK("its configuration there sets conf.secrets.repo.url").Run(x)
	if err != nil {
		return err
	}
	if !x.Options.DryRun && strings.TrimSpace(res.Stdout) == "" {
		return fmt.Errorf("%s does not set conf.secrets.repo.url for %s: add it, with conf.fleet.repo.url, the signers file and support's key, and push", ref, x.Value("host"))
	}
	return nil
}

// privateFetch is how a host that does not run a fleet configuration yet
// fetches its private one, before the switch: with its own configuration
// key, GitHub's host keys pinned as fleet-manager carries them
// (lib.forgeHostKeys, written by writePinnedKeys), as the sync will.
const privateFetch = "ssh -i /var/lib/fleet/config_deploy_key -o IdentitiesOnly=yes " +
	"-o GlobalKnownHostsFile=/var/lib/fleet/known_hosts.pinned -o HostKeyAlgorithms=%s " +
	"-o UserKnownHostsFile=/var/lib/fleet/known_hosts -o StrictHostKeyChecking=accept-new"

// installerAttr is fleet-manager at the commit checked for the installer,
// for nix run on the host.
func installerAttr(x *engine.Ctx, attr string) string {
	return "git+" + fleet.UpstreamURL + "?rev=" + x.Value("installer_rev") + "#" + attr
}

// writePinnedKeys writes GitHub's pinned host keys, from the checked
// fleet-manager commit, where privateFetch reads them, and sets the
// GIT_SSH_COMMAND value for the host's shell as ssh_private.
func writePinnedKeys(x *engine.Ctx) error {
	if x.Value("ssh_private") != "" {
		return nil
	}
	keys, err := engine.Cmd("nix", "eval", "--raw", installerAttr(x, "lib.forgeHostKeys.text")).
		ReadOnly().Quiet().OK("read GitHub's pinned host keys from fleet-manager at {{.installer_rev}}").Run(x)
	if err != nil {
		return err
	}
	algs, err := engine.Cmd("nix", "eval", "--raw", installerAttr(x, "lib.forgeHostKeys.algorithms")).ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	x.Values["pinned_keys"] = keys.Stdout
	if _, err := fleet.OnAdmin(`sudo -n install -d -m 0700 /var/lib/fleet && sudo -n sh -c 'cat > /var/lib/fleet/known_hosts.pinned'`).
		StdinFrom("pinned_keys").StdinNote("GitHub's pinned host keys").OK("wrote them to /var/lib/fleet/known_hosts.pinned").Run(x); err != nil {
		return err
	}
	x.Values["ssh_private"] = fmt.Sprintf(privateFetch, strings.TrimSpace(algs.Stdout))
	return nil
}

// createIdentity runs fleet-secrets-sync init: the host's own, or, on a host
// without fleet's tools yet, the one of the fleet-manager commit checked for
// the switch, with nix run.
func createIdentity(x *engine.Ctx) error {
	cmd := "sudo -n fleet-secrets-sync init --state /var/lib/fleet --host {{.host}}"
	if x.Value("has_tooling") != "true" {
		if x.Value("installer_rev") == "" {
			// --switch-to named the configuration; the tools come from main's
			// newest commit, once a key you trust has signed it.
			rev, err := signedUpstreamMain(x)
			if err != nil {
				return err
			}
			x.Values["installer_rev"] = rev
		}
		x.Values["init_flake"] = engine.Quote(installerAttr(x, "fleet-secrets-sync"))
		cmd = "sudo -n nix --extra-experimental-features 'nix-command flakes' run {{.init_flake}} -- init --state /var/lib/fleet --host {{.host}}"
	}
	_, err := fleet.OnAdmin(cmd).OK("created under /var/lib/fleet").
		After(func(x *engine.Ctx) error {
			x.Kept("identity", "{{.host}}'s new identity in /var/lib/fleet: the next run uses it, and removing it would lock the host out of the secrets it gets enrolled for")
			return nil
		}).Run(x)
	return err
}

// moving says whether the host runs a fleet configuration from another
// repository than the one this configuration names: it has a checkout, and
// that checkout's origin is not conf.fleet.repo.url.
func moving(x *engine.Ctx) bool {
	origin, url := x.Value("origin"), x.Value("config_url")
	return x.Value("has_tooling") == "true" && x.Value("has_checkout") == "true" && origin != "" && url != "" && origin != url
}

// needsSwitch is whether the host is to be switched to its configuration in
// this repository: it does not run a fleet configuration yet, or runs one from
// another repository.
func needsSwitch(x *engine.Ctx) (bool, string) {
	switch {
	case x.Value("has_tooling") != "true", moving(x):
		return true, ""
	case x.Value("origin") != "" && x.Value("origin") == x.Value("config_url"):
		return false, "it already runs a configuration with secrets from " + x.Value("config_url")
	}
	return false, "it already runs a configuration with secrets"
}

// The read-only commands that list what /etc/nixos holds, for the person to
// see: a checkout, or plain files.
const (
	checkoutListing = `g() { sudo -n git -c safe.directory=/etc/nixos -C /etc/nixos "$@"; }
echo "origin: $(g remote get-url origin 2>/dev/null)"
b=$(g symbolic-ref -q --short HEAD 2>/dev/null) && echo "branch: $b" || echo "detached at $(g rev-parse --short HEAD 2>/dev/null)"
echo "uncommitted or untracked entries: $(g status --porcelain 2>/dev/null | wc -l)"`
	filesListing = `sudo -n ls -A /etc/nixos | head -20
n=$(sudo -n ls -A /etc/nixos | wc -l); if [ "$n" -gt 20 ]; then echo "and $((n - 20)) more"; fi`
)

// warnsAboutEtcNixos: the switch is to come, and /etc/nixos holds something.
func warnsAboutEtcNixos(x *engine.Ctx) (bool, string) {
	if ok, why := needsSwitch(x); !ok {
		return false, why
	}
	if k := x.Value("etc_nixos"); k != "git" && k != "files" {
		return false, "/etc/nixos is empty; the first-boot clone makes the checkout"
	}
	return true, ""
}

// warnAboutEtcNixos shows what /etc/nixos holds and asks before the switch,
// which is followed by deleting it. It changes nothing. What the host prints
// is shown and never kept in a value: it is the host's own text, and the
// commands after this one are built from values (see enrollFacts).
func warnAboutEtcNixos(x *engine.Ctx) error {
	listing := filesListing
	if x.Value("etc_nixos") == "git" {
		listing = checkoutListing
	}
	res, err := fleet.OnAdmin(listing).ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	if shown := strings.TrimRight(res.Stdout, "\n"); shown != "" {
		x.UI.Text(shown)
	}
	host := x.Value("host")
	if moving(x) {
		x.UI.Note(host + " moves from " + x.Value("origin") + " to " + x.Value("config_url") + ".")
	}
	x.UI.Note("Everything in /etc/nixos on " + host + " is deleted after the switch, and the fleet's checkout takes its place. " +
		"Copy anything you still need first, such as hardware-configuration.nix, into the host's folder in the configuration repository; " +
		"nixos-generate-config --show-hardware-config prints it again.")
	ok, err := x.Approve("Delete what /etc/nixos holds after the switch?")
	if errors.Is(err, engine.ErrStopped) {
		x.UI.Note("Nothing was changed on " + host + ": /etc/nixos is left as it is.")
	}
	if err != nil || !ok {
		return err
	}
	return nil
}

//go:embed remake-etc-nixos.sh
var remakeEtcNixosScript string

// remakeEtcNixos replaces /etc/nixos with the configuration repository's
// checkout, made by the first-boot clone (remake-etc-nixos.sh), and checks
// that it is at the commit the host was switched to. The script runs as a
// unit on the host, as the switch does: a dropped connection must not leave
// /etc/nixos deleted with no clone started.
func remakeEtcNixos(x *engine.Ctx) error {
	if x.Value("config_url") == "" {
		return errors.New("could not read conf.fleet.repo.url from " + x.Value("flake"))
	}
	x.Values["remake_script"] = remakeEtcNixosScript
	x.Values["remake_args"] = engine.Quote(x.Value("config_url")) + " " + map[bool]string{true: "true", false: "false"}[x.Value("etc_nixos") == "git"]
	if _, err := fleet.OnAdmin("sudo -n sh -c 'umask 077 && cat > /run/fleet-enroll-remake.sh'").
		StdinFrom("remake_script").StdinNote("remake-etc-nixos.sh").Run(x); err != nil {
		return err
	}
	if _, err := fleet.StillRunning(fleet.OnAdmin(`sudo -n systemctl reset-failed fleet-enroll-remake 2>/dev/null; `+
		`sudo -n systemd-run --wait --collect --unit=fleet-enroll-remake `+
		`--setenv=PATH=/run/wrappers/bin:/run/current-system/sw/bin `+
		`sh /run/fleet-enroll-remake.sh {{.remake_args}}; s=$?; `+
		`sudo -n journalctl -u fleet-enroll-remake -o cat -n 15 --no-pager; `+
		`sudo -n rm -f /run/fleet-enroll-remake.sh; exit $s`).OK("/etc/nixos is the checkout of {{.config_url}}"),
		"the new checkout", "fleet-enroll-remake", "{{.admin}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	res, err := fleet.OnAdmin("sudo -n git -c safe.directory=/etc/nixos -C /etc/nixos rev-parse HEAD").ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	lines := strings.Fields(res.Stdout)
	if len(lines) == 0 {
		return nil
	}
	head := lines[len(lines)-1]
	x.Values["origin"], x.Values["has_checkout"] = x.Value("config_url"), "true"
	switch rev := x.Value("switch_rev"); {
	case rev == "":
	case head == rev:
		x.UI.OK("it is at " + head + ", the commit the host was switched to")
	default:
		x.UI.Note("It is at " + head + ", not at " + rev + ", the commit the host was switched to: the first-boot clone leaves the tip of the branch when that commit is not on it. The sync converges.")
	}
	return nil
}

func switchHost(x *engine.Ctx) error {
	x.Values["switch_env"] = ""
	if sshURL(x.Value("config_url")) && strings.HasPrefix(x.Value("switch_ref"), "git+ssh://") {
		// A private configuration: nix fetches it with the host's own key.
		if err := writePinnedKeys(x); err != nil {
			return err
		}
		x.Values["switch_env"] = engine.Quote("--setenv=GIT_SSH_COMMAND=" + x.Value("ssh_private"))
	}
	// systemd-run gives the service a minimal PATH; nixos-rebuild needs the
	// system's (it runs `test`).
	// It runs as a unit on the host, so that a dropped connection or a stop
	// here cannot cut it in half.
	_, err := fleet.StillRunning(fleet.OnAdmin(`sudo -n systemctl reset-failed fleet-enroll-switch 2>/dev/null; `+
		`sudo -n systemd-run --wait --collect --unit=fleet-enroll-switch `+
		`--setenv=PATH=/run/wrappers/bin:/run/current-system/sw/bin {{.switch_env}} `+
		`nixos-rebuild switch --flake {{.switch_target}}; s=$?; `+
		`sudo -n journalctl -u fleet-enroll-switch -o cat -n 15 --no-pager; exit $s`).OK("switched"),
		"the switch", "fleet-enroll-switch", "{{.admin}}").Run(x)
	if err == nil {
		x.Values["switched"] = "true"
	}
	return err
}

// signedUpstreamMain takes the newest commit of fleet-manager's main, fetched with
// its whole history, and returns it once it and every commit since the one you
// trusted last are signed by a key in your copy of fleet-manager's keys (fleet
// trust signers): a host's own sync takes no other, so neither does its first
// switch. The record then moves to it.
func signedUpstreamMain(x *engine.Ctx) (string, error) {
	dir, signers, cleanup, err := fleetManagerHistory(x)
	if err != nil {
		return "", err
	}
	defer cleanup()
	rev, err := fleet.CommitOf(x, dir, "refs/remotes/origin/main")
	if err != nil {
		if x.Options.DryRun {
			return "<newest commit of main>", nil
		}
		return "", err
	}
	x.Values["upstream_rev"] = rev
	if err := verifyPinIn(x, dir, signers, rev, ""); err != nil {
		return "", fmt.Errorf("not switching %s to main's newest commit: %w", x.Value("host"), err)
	}
	if err := fleet.CheckTrusted(x, dir, fleet.UpstreamURL, "main", rev); err != nil {
		return "", err
	}
	return rev, nil
}

func copyEnrollment(x *engine.Ctx) error {
	res, err := fleet.OnAdmin(`sudo -n cat /var/lib/fleet/{{.host}}.enroll && sudo -n sha256sum /var/lib/fleet/{{.host}}.enroll`).
		RunQuiet(x)
	if err != nil {
		return err
	}
	doc, sumLine, ok := cutLast(res.Stdout)
	if !ok {
		return errors.New("unexpected answer from the host")
	}
	remote := strings.Fields(sumLine)[0]
	local := sha256.Sum256([]byte(doc))
	if hex.EncodeToString(local[:]) != remote {
		return fmt.Errorf("checksums differ: %s here, %s on the host", hex.EncodeToString(local[:])[:16], remote[:16])
	}
	// Next to the secrets checkout, whose .gitignore keeps *.enroll out.
	path := filepath.Join(x.Value("repo"), x.Value("host")+".enroll")
	x.Values["from"] = path
	x.Values["enroll_doc"] = doc
	for _, line := range strings.Split(doc, "\n") {
		if v, ok := strings.CutPrefix(line, "age_recipient: "); ok {
			x.Values["recipient"] = v
		}
		if v, ok := strings.CutPrefix(strings.TrimPrefix(line, "secrets_"), "deploy_pubkey: "); ok {
			x.Values["deploy_key"] = v
		}
		if v, ok := strings.CutPrefix(line, "config_deploy_pubkey: "); ok {
			x.Values["config_key"] = v
		}
	}
	if x.Options.DryRun {
		x.UI.Skip("dry run: checksums match (" + remote[:16] + "…), not copied to " + path)
		return nil
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return err
	}
	x.UI.OK("copied to " + path + ", checksum " + remote[:16] + "… on both sides")
	return nil
}

// cutLast splits off the last line.
func cutLast(s string) (string, string, bool) {
	s = strings.TrimRight(s, "\n")
	i := strings.LastIndex(s, "\n")
	if i < 0 {
		return "", "", false
	}
	return s[:i+1], s[i+1:], true
}

// enrolledWithThisIdentity looks at the host's branch: none means it is not
// enrolled; its file encrypted to this identity, that it is, already.
func enrolledWithThisIdentity(x *engine.Ctx) (bool, string) {
	host := x.Value("host")
	ref := secrets.BranchRef(x, host)
	if ref == "" {
		return true, ""
	}
	recipients, err := secrets.Recipients(x, ref, host)
	if err != nil {
		// Not readable: host-enroll says what is wrong with it.
		return true, ""
	}
	if slices.Contains(recipients, x.Value("recipient")) {
		return false, "it is enrolled already, with this identity"
	}
	recovery, _ := secrets.ReadRecoveryRecipient()
	for _, r := range recipients {
		if r != recovery {
			x.Values["enrolled_with"] = r
			break
		}
	}
	return true, ""
}

func runHostEnroll(x *engine.Ctx) error {
	if other := x.Value("enrolled_with"); other != "" {
		return fmt.Errorf("%s is enrolled with another identity (%s…); if it was reinstalled, run fleet secrets host-rekey %s --from %s",
			x.Value("host"), other[:12], x.Value("host"), x.Value("from"))
	}
	args := []string{"secrets", "host-enroll", "{{.host}}", "--from", "{{.from}}", "--repo", "{{.repo}}", "--recovery", "{{.recovery}}"}
	if x.Options.Yes {
		args = append(args, "--yes")
	}
	_, err := engine.Cmd("fleet", args...).ReadOnly().Interactive().OK("enrolled").Run(x)
	return err
}

// aheadOfUpstream is whether the host's branch has commits origin's copy
// lacks, or origin has none.
func aheadOfUpstream(x *engine.Ctx) (bool, string) {
	branch := "host/" + x.Value("host")
	out, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-list", "--count",
		"refs/remotes/origin/"+branch+"..refs/heads/"+branch)
	if err == nil && out == "0" {
		return false, "nothing to push"
	}
	return true, ""
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?$`)

// sshURL says whether a git URL is an SSH address, git@host:… or ssh://…,
// which a host fetches with its own key, as modules/linux/fleet/config-fetch.nix
// decides.
func sshURL(u string) bool { return strings.HasPrefix(u, "ssh://") || scpURL.MatchString(u) }

var scpURL = regexp.MustCompile(`^[^/:]+@[^/:]+:`)

func registerDeployKey(x *engine.Ctx) error {
	origin, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "remote", "get-url", "origin")
	return registerKey(x, "deploy_key", origin, "the secrets repository")
}

// registerConfigKey registers the configuration key on the repository the
// host's configuration fetches, conf.fleet.repo.url.
func registerConfigKey(x *engine.Ctx) error {
	if x.Value("config_key") == "" && !x.Options.DryRun {
		return errors.New("its enrollment file lists no configuration key: the fleet-secrets-sync it runs predates them;" +
			" let it sync a newer fleet-manager, whose init makes one, then run this again")
	}
	return registerKey(x, "config_key", x.Value("config_url"), "the configuration repository")
}

// registerKey adds the public key in the value keyValue to the GitHub
// repository at remote as a read-only deploy key, with gh, or has the person
// add it on the web page; what names the repository in what is said.
func registerKey(x *engine.Ctx, keyValue, remote, what string) error {
	key := x.Value(keyValue)
	if x.Options.DryRun && key == "" {
		key = "<" + strings.ReplaceAll(keyValue, "_", " ") + ">"
	}
	x.Values["key_line"] = key
	x.UI.Text(key)
	if cmd := x.Value("register-with"); cmd != "" {
		_, err := engine.Cmd("sh", "-c", cmd).StdinFrom("key_line").StdinNote("the public key").Run(x)
		return err
	}
	m := githubRemote.FindStringSubmatch(remote)
	_, noGh := x.LookPath("gh")
	if noGh == nil && m != nil {
		_, noGh = engine.Silently(x, nil, "gh", "auth", "status")
	}
	if noGh != nil || m == nil {
		x.UI.Note("Add it on the forge: " + what + " → Settings → Deploy keys → Add deploy key, title " +
			x.Value("host") + ", with write access left unticked.")
		return engine.WaitFor(x, "Added it, read-only?")
	}
	x.Values["slug"] = m[1]
	keys, err := deployKeys(x, m[1])
	if err != nil {
		return err
	}
	body := strings.Join(strings.Fields(key)[:2], " ")
	for _, k := range keys {
		if k.Key == body {
			if !k.ReadOnly {
				return fmt.Errorf("the key is registered as %q but with write access: remove it and run this again", k.Title)
			}
			x.UI.OK("registered already, read-only, as " + k.Title)
			return nil
		}
	}
	tmp, err := os.CreateTemp("", "fleet-deploy-*.pub")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(key + "\n")
	tmp.Close()
	x.Values["key_file"] = tmp.Name()
	if _, err := engine.Cmd("gh", "repo", "deploy-key", "add", "{{.key_file}}", "--repo", "{{.slug}}", "--title", "{{.host}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	keys, err = deployKeys(x, m[1])
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.Key == body && k.ReadOnly {
			x.UI.OK("GitHub has it read-only, as " + k.Title)
			return nil
		}
	}
	return errors.New("GitHub does not list the key as read-only")
}

type deployKey struct {
	Title    string `json:"title"`
	Key      string `json:"key"`
	ReadOnly bool   `json:"read_only"`
}

func deployKeys(x *engine.Ctx, slug string) ([]deployKey, error) {
	res, err := engine.Cmd("gh", "api", "repos/"+slug+"/keys").RunQuiet(x)
	if err != nil {
		return nil, err
	}
	var keys []deployKey
	return keys, json.Unmarshal([]byte(res.Stdout), &keys)
}

// fitsScript runs on the host with: whether the configuration boots with
// UEFI, then pairs of a mount point and the device the configuration names
// for it. It prints a line for each that does not hold.
const fitsScript = `set -u
efi=$1; shift
if [ "$efi" = true ] && [ ! -d /sys/firmware/efi ]; then echo "noefi"; fi
while [ $# -ge 2 ]; do
  m=$1 want=$2; shift 2
  have=$(findmnt -no SOURCE "$m" 2>/dev/null | sed 's/\[.*//') || have=""
  if [ -z "$have" ]; then echo "missing $m $want"; continue; fi
  [ "$(readlink -f "$want")" = "$(readlink -f "$have")" ] || echo "differs $m $want $have"
done`

// checkFits compares what the configuration mounts at / and /boot, and
// whether it boots with UEFI, with the host as it is.
func checkFits(x *engine.Ctx) error {
	res, err := engine.Cmd("nix", "eval", "--json", x.Value("eval_flake")+"#nixosConfigurations.{{.host}}.config", "--apply",
		`c: { fs = builtins.mapAttrs (_: f: f.device) (builtins.intersectAttrs { "/" = null; "/boot" = null; } c.fileSystems); `+
			`efi = c.boot.loader.systemd-boot.enable || (c.boot.loader.grub.enable && c.boot.loader.grub.efiSupport); `+
			`disko = c.conf.hardware.disk.enable; }`).
		ReadOnly().Quiet().OK("read its filesystems and boot loader").Run(x)
	if err != nil || x.Options.DryRun && res.Stdout == "" {
		return err
	}
	var want struct {
		FS    map[string]string `json:"fs"`
		EFI   bool              `json:"efi"`
		Disko bool              `json:"disko"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &want); err != nil {
		return fmt.Errorf("%s's filesystems: %w", x.Value("host"), err)
	}
	args := []string{fmt.Sprint(want.EFI)}
	for _, m := range []string{"/", "/boot"} {
		if dev := want.FS[m]; strings.HasPrefix(dev, "/dev/") {
			args = append(args, engine.Quote(m), engine.Quote(dev))
		}
	}
	x.Values["fits_args"], x.Values["fits_script"] = strings.Join(args, " "), fitsScript
	out, err := fleet.OnAdmin("sh -s -- {{.fits_args}}").StdinFrom("fits_script").StdinNote("the check").ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	var problems []string
	for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 1 && f[0] == "noefi":
			problems = append(problems, "the configuration boots with UEFI, and "+x.Value("host")+" does not")
		case len(f) == 3 && f[0] == "missing":
			problems = append(problems, "the configuration mounts "+f[2]+" at "+f[1]+", and nothing is mounted there on "+x.Value("host"))
		case len(f) == 4 && f[0] == "differs":
			problems = append(problems, "the configuration mounts "+f[2]+" at "+f[1]+", and "+x.Value("host")+" has "+f[3]+" there")
		}
	}
	if len(problems) > 0 {
		msg := strings.Join(problems, "; ")
		if want.Disko {
			msg += ". Its configuration has the disk layout os-install makes (conf.hardware.disk.enable): " +
				"a machine installed otherwise needs conf.hardware.disk.enable = false and its own hardware-configuration.nix"
		}
		return errors.New(msg + "; not switching it")
	}
	x.UI.OK("its filesystems and boot loader match " + x.Value("host"))
	return nil
}
