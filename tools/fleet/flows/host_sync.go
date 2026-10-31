package flows

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// HostSync has one host sync now.
var HostSync = &engine.Flow{
	Path:  []string{"host", "sync"},
	Title: "Sync a host now",
	Short: "Have a host fetch its configuration and secrets now, rather than at its next timer, and check what it then runs",
	Long: "Logs in to the host as support, with nothing forwarded, and starts its sync\n" +
		"at once, instead of waiting for its timer: the host fetches its branch and its\n" +
		"secrets, checks that they are signed by a key it trusts, and switches to what\n" +
		"it gets. fleet waits for the sync to end, shows the host's log if it failed,\n" +
		"and checks that the host now runs the newest commit of its branch. Then it\n" +
		"reports the same as fleet host check: configuration, secrets and health.\n" +
		"It stops at the first step that fails.",
	Example: "  fleet host sync pi1\n  fleet host sync pi-gw --address 192.168.0.9",
	Args:    []string{"host"},
	Inputs:  fleet.HostInputs(),
	Needs:   []string{"ssh", "git", "nix"},
	Steps: []engine.Step{
		hostReachStep,
		{
			Title: "See what it runs and what is published",
			Why:   "The sync should take the host to the newest commit of the branch it tracks; the host's revision now, and that commit, are what the check afterwards compares.",
			Do:    engine.Func(seeWhatIsPublished),
		},
		{
			Title: "Sync it",
			Why:   "Starts the host's sync and waits until it ends. It fetches the configuration and the secrets, and activation applies them; a commit no key it trusts signed is refused.",
			Do:    engine.Func(startHostSync),
		},
		{
			Title: "Check what it runs now",
			Why:   "After the sync the host should run the commit the branch was at.",
			Do:    engine.Func(checkAfterSync),
		},
		hostConfigStep,
		hostSecretsStep,
		hostHealthStep,
	},
}

// hostRevision is the revision the host runs.
func hostRevision(x *engine.Ctx) (string, error) {
	res, err := fleet.OnHost("cat /etc/org/revision").RunQuiet(x)
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(res.Stdout)
	if rev == "" || rev == "unknown" {
		return "", errors.New("the system records no revision (installed from uncommitted changes?)")
	}
	return rev, nil
}

func seeWhatIsPublished(x *engine.Ctx) error {
	running, err := hostRevision(x)
	if err != nil {
		return err
	}
	x.Values["running_rev"] = running
	res, err := fleet.Nix("eval", "--raw", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.fleet.repo.url").RunQuiet(x)
	if err != nil {
		return fmt.Errorf("reading %s's repository from the configuration: %w", x.Value("host"), err)
	}
	if strings.TrimSpace(res.Stdout) == "" {
		return fmt.Errorf("%s syncs from no repository: there is nothing to sync", x.Value("host"))
	}
	res, err = fleet.Nix("eval", "--raw", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.fleet.repo.ref").RunQuiet(x)
	if err != nil {
		return fmt.Errorf("reading the branch %s tracks from the configuration: %w", x.Value("host"), err)
	}
	ref := strings.TrimSpace(res.Stdout)
	x.Values["repo_ref"] = ref
	expected := publishedTip(x, ref)
	x.Values["expected_rev"] = expected
	if expected == "" {
		x.UI.OK(x.Value("host") + " runs " + short(running) + "; the newest commit of " + ref + " is not known: the sync applies any new secrets and checks the rest")
		return nil
	}
	if running == expected {
		x.UI.OK(fmt.Sprintf("%s runs %s, the newest commit of %s: the sync applies any new secrets and checks the rest", x.Value("host"), short(running), ref))
		return nil
	}
	x.UI.OK(fmt.Sprintf("%s runs %s; %s is at %s: the sync takes it there", x.Value("host"), short(running), ref, short(expected)))
	return nil
}

// publishedTip is the newest commit of ref as the flake's repository has it,
// or "" with a note when it cannot be read: the flake is not a checkout, or
// has no such branch.
func publishedTip(x *engine.Ctx, ref string) string {
	flake := x.Value("flake")
	if _, err := os.Stat(filepath.Join(flake, ".git")); err != nil {
		x.UI.Note(flake + " is not a git checkout, so the published commit is not known; the host's revision afterwards is not compared")
		return ""
	}
	remote := "refs/remotes/origin/" + ref
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "fetch", "-q", "origin", "+refs/heads/"+ref+":"+remote).RunQuiet(x); err != nil {
		x.UI.Note("could not fetch " + ref + " from origin, so the published commit is not known: " + err.Error())
		return ""
	}
	res, err := engine.Cmd("git", "-C", "{{.flake}}", "rev-parse", remote).RunQuiet(x)
	if err != nil || strings.TrimSpace(res.Stdout) == "" {
		x.UI.Note("origin has no " + ref + ", so the published commit is not known")
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// startHostSync starts the host's sync and waits for it. When it fails, the
// unit's log is shown.
func startHostSync(x *engine.Ctx) error {
	_, err := fleet.StillRunning(fleet.OnHost("sudo -n systemctl start config-auto-update"), "the sync", "config-auto-update", "{{.user}}").Run(x)
	if err == nil {
		return nil
	}
	if x.Context.Err() != nil {
		return err
	}
	x.UI.Fail(err.Error())
	showSyncLog(x)
	return fmt.Errorf("the sync failed on %s; the log above, and `ssh %s@%s journalctl -u config-auto-update` for all of it", x.Value("host"), x.Value("user"), x.Value("address"))
}

// showSyncLog shows the end of the sync unit's log.
func showSyncLog(x *engine.Ctx) {
	res, err := fleet.OnHost("sudo -n journalctl -u config-auto-update -n 30 --no-pager -o cat").RunQuiet(x)
	if err != nil {
		x.UI.Note("could not read the log: " + err.Error())
		return
	}
	if log := strings.TrimRight(res.Stdout, "\n"); log != "" {
		x.UI.Text(log)
	}
}

func checkAfterSync(x *engine.Ctx) error {
	ref := x.Value("repo_ref")
	return checkRunning(x, x.Value("running_rev"), x.Value("expected_rev"), "the newest commit of "+ref)
}

// checkRunning reads the host's revision and compares it with the commit it
// should run, described by what; before is the revision it ran before the
// sync, if known. With no expected commit it only says what the host runs.
func checkRunning(x *engine.Ctx, before, expected, what string) error {
	now, err := hostRevision(x)
	if err != nil {
		return err
	}
	host := x.Value("host")
	switch {
	case expected == "":
		x.UI.OK(host + " runs " + short(now))
		return nil
	case now == expected:
		x.UI.OK(host + " now runs " + short(now) + ", " + what)
		return nil
	}
	verb := "now runs"
	if now == before {
		verb = "still runs"
	}
	showSyncLog(x)
	return fmt.Errorf("%s %s %s, not %s (%s): the log above says why, and fleet host check shows a problem the host recorded",
		host, verb, short(now), short(expected), what)
}
