package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// InventoryPin moves a private inventory to a newer fleet-manager: it changes
// the commit that the inventory's flake.lock pins for fleet-manager, checks
// that every host still evaluates, and commits and pushes the lock. Hosts
// fetch their own branches, so the inventory is published afterwards.
var InventoryPin = &engine.Flow{
	Path:  []string{"inventory", "pin"},
	Title: "Pin a newer fleet-manager in the inventory",
	Short: "Pin fleet-manager's newest trusted commit, or a given one, in the inventory, check every host, commit and push",
	Long: "An inventory builds its hosts from fleet-manager, at the commit its flake.lock pins.\n" +
		"This moves that pin. fleet brings its clone of fleet-manager up to date, which only\n" +
		"takes a commit signed by a key you trust, and pins main's newest commit, or the one\n" +
		"you give with --to. It shows the commits the pin moves over, and asks before it pins\n" +
		"an older one. Then it locks the new commit and evaluates every host, so that a host\n" +
		"the new fleet-manager breaks is found now, not on the host. If all of them evaluate,\n" +
		"it commits flake.lock, signed, and pushes it once you approve. The hosts take the new\n" +
		"fleet-manager when you publish the inventory after that.",
	Example: "  fleet inventory pin --flake inventory\n" +
		"  fleet inventory pin --to 3f2a9c1 --flake inventory",
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "to", Prompt: "Commit to pin", Help: "A fleet-manager commit or branch to pin; its newest main by default", Optional: true},
	},
	Needs: []string{"git", "nix"},
	Steps: []engine.Step{
		{
			Title: "Check the inventory",
			Why: "The pin is committed on top of its main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. " +
				"The pin it holds now is read from flake.lock.",
			Do: engine.Func(checkPinSource),
		},
		{
			Title: "Bring fleet-manager's clone up to date",
			Why: "The new pin is taken from the tool's own clone of fleet-manager, fetched here. A commit is pinned only if a key you trust signed it " +
				"and it is on the line of main that you trusted.",
			Do: engine.Func(chooseNewPin),
		},
		unlessPinned(engine.Step{
			Title: "See what changes",
			Why: "The commits between the old pin and the new one, so that you know what the hosts will get. Pinning an older commit is asked about first. " +
				"The hosts are evaluated as they are now, to compare with after.",
			Do: engine.Func(seeWhatChanges),
		}),
		unlessPinned(engine.Step{
			Title: "Pin it",
			Why:   "Locks fleet-manager at the new commit in flake.lock, and nothing else. If the run stops before the commit, flake.lock is put back.",
			Do:    engine.Func(lockNewPin),
		}),
		unlessPinned(engine.Step{
			Title: "Check every host still evaluates",
			Why:   "A new fleet-manager may break a host. Each host is evaluated again, and the ones whose system changes are listed.",
			Do:    engine.Func(checkHostsAfterPin),
		}),
		unlessPinned(engine.Step{
			Title: "Commit the pin",
			Why:   "One commit that changes flake.lock only. The repository's own git settings sign it.",
			Do:    engine.Func(commitPin),
		}),
		unlessPinned(engine.Step{
			Title: "Push it",
			Why:   "Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.",
			Do:    engine.Func(pushPin),
		}),
		unlessPinned(engine.Step{
			Title: "Next",
			Why:   "The pin is on main only. Each host fetches its own branch, so it gets the new fleet-manager once you publish.",
			Do: engine.Show("Hosts fetch their own branches, not main: publish them now, and each host takes the new fleet-manager at its next sync:\n" +
				"  fleet inventory publish --flake {{.flake_given}}\n" +
				"A host can be told to sync at once with: ssh support@<host> sudo systemctl start config-auto-update"),
		}),
	},
}

// unlessPinned leaves a step out when the inventory pins the commit already.
func unlessPinned(s engine.Step) engine.Step {
	s.When = func(x *engine.Ctx) (bool, string) {
		if x.Value("done") == "true" {
			return false, "the inventory pins " + fleet.Short(x.Value("new_pin")) + " already"
		}
		return true, ""
	}
	return s
}

func pinGit(args ...string) *engine.Command {
	return engine.Cmd("git", append([]string{"-C", "{{.flake}}"}, args...)...).ReadOnly().Quiet()
}

// pinnedFleetManager reads the commit and the address that flake.lock pins
// fleet-manager at.
func pinnedFleetManager(flake string) (rev, url string, err error) {
	data, err := os.ReadFile(filepath.Join(flake, "flake.lock"))
	if err != nil {
		return "", "", err
	}
	var lock struct {
		Nodes map[string]struct {
			Locked struct {
				Type string `json:"type"`
				URL  string `json:"url"`
				Rev  string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return "", "", fmt.Errorf("%s/flake.lock: %w", flake, err)
	}
	n, ok := lock.Nodes["fleet-manager"]
	if !ok {
		return "", "", errors.New("the inventory has no fleet-manager input")
	}
	if n.Locked.Type != "git" || n.Locked.URL == "" || n.Locked.Rev == "" {
		return "", "", fmt.Errorf("the inventory pins fleet-manager as a %q input, not a git one with a commit: pin it by hand", n.Locked.Type)
	}
	return n.Locked.Rev, n.Locked.URL, nil
}

func checkPinSource(x *engine.Ctx) error {
	flake := x.Value("flake")
	if _, err := os.Stat(filepath.Join(flake, ".git")); err != nil {
		return fmt.Errorf("%s is not a git checkout of the inventory", flake)
	}
	x.Values["flake_given"] = flake
	if n := x.Value("flake_name"); n != "" {
		x.Values["flake_given"] = n
	}
	if _, err := pinGit("status", "--porcelain", "--untracked-files=no").Expect(func(r engine.Result) error {
		if strings.TrimSpace(r.Stdout) != "" {
			return errors.New("the checkout has uncommitted changes: commit or discard them first")
		}
		return nil
	}).OK("no uncommitted changes").Run(x); err != nil {
		return err
	}
	if _, err := pinGit("remote", "get-url", "origin").Capture("inv_origin").OK("the checkout fetches {{.inv_origin}}").Run(x); err != nil {
		return err
	}
	origin := x.Value("inv_origin")
	if _, err := pinGit("fetch", "-q", "origin", "main").OK("fetched origin's main").Run(x); err != nil {
		return err
	}
	head, err := fleet.CommitOf(x, flake, "HEAD")
	if err != nil {
		return err
	}
	main, err := fleet.CommitOf(x, flake, "refs/remotes/origin/main")
	if err != nil {
		return err
	}
	if head != main {
		behind, err := fleet.Follows(x, flake, head, main)
		if err != nil {
			return err
		}
		if behind {
			return errors.New("it is behind origin's main: pull first")
		}
		ahead, err := fleet.Follows(x, flake, main, head)
		if err != nil {
			return err
		}
		if ahead {
			return errors.New("it has commits origin's main does not: push them, or set them aside, first")
		}
		return errors.New("it and origin's main have gone apart")
	}
	x.UI.OK("the checkout is level with origin's main, " + fleet.Short(head))
	signers, err := trustedSignersFor(x, origin, flake)
	if err != nil {
		return err
	}
	x.Values["signers"], x.Values["inv_head"] = signers, head
	if _, err := pinGit("-c", "gpg.ssh.allowedSignersFile={{.signers}}", "verify-commit", "HEAD").
		Explain(func(engine.Result) string {
			return "the newest commit, {{.inv_head}}, is not signed by a key you trust ({{.signers}})"
		}).OK("{{.inv_head}} is signed by a key you trust").Run(x); err != nil {
		return err
	}
	if err := fleet.CheckTrusted(x, flake, origin, "main", head); err != nil {
		return err
	}
	rev, url, err := pinnedFleetManager(flake)
	if err != nil {
		return err
	}
	have, err1 := fleet.RepoID(url)
	want, err2 := fleet.RepoID(fleet.UpstreamURL)
	if err1 != nil || err2 != nil || have != want {
		return fmt.Errorf("it pins fleet-manager from %s, not %s: pin that one by hand", url, fleet.UpstreamURL)
	}
	x.Values["old_pin"] = rev
	x.UI.OK("fleet-manager is pinned at " + fleet.Short(rev))
	return nil
}

func chooseNewPin(x *engine.Ctx) error {
	id, err := fleet.RepoID(fleet.UpstreamURL)
	if err != nil {
		return err
	}
	names, err := fleet.ReadNames()
	if err != nil {
		return err
	}
	name := ""
	for n, i := range names {
		if i == id {
			name = n
		}
	}
	if name == "" {
		return errors.New("fleet repo add first: run it with no address and it adds fleet-manager")
	}
	clone, err := fleet.ClonePathOfID(id)
	if err != nil {
		return err
	}
	if !fileExists(clone) {
		return fmt.Errorf("the clone of %s is not in %s: run fleet repo add again", name, clone)
	}
	x.Values["fm_clone"] = clone
	x.UI.OK(name + " is the tool's clone in " + clone)
	if err := fleet.SyncClone(x, clone); err != nil {
		return err
	}
	origin, err := fleet.OriginOf(x, clone)
	if err != nil {
		return err
	}
	var pin string
	if to := x.Value("to"); to == "" {
		if pin, err = fleet.CommitOf(x, clone, "refs/remotes/origin/main"); err != nil {
			return err
		}
	} else {
		if strings.HasPrefix(to, "-") {
			return fmt.Errorf("%q is not a commit or a branch", to)
		}
		if pin, err = fleet.CommitOf(x, clone, to); err != nil {
			var err2 error
			if pin, err2 = fleet.CommitOf(x, clone, "refs/remotes/origin/"+to); err2 != nil {
				return fmt.Errorf("%s is neither a commit nor a branch in fleet-manager's clone", to)
			}
		}
		x.Values["fm_pin"] = pin
		if _, err := engine.Cmd("git", "-C", "{{.fm_clone}}", "verify-commit", "{{.fm_pin}}").ReadOnly().Quiet().
			Explain(func(engine.Result) string {
				return "{{.fm_pin}} is not signed by a key you trust: git checks it against the clone's gpg.ssh.allowedSignersFile setting"
			}).OK("{{.fm_pin}} is signed by a key you trust").Run(x); err != nil {
			return err
		}
		if err := fleet.CheckTrustedLine(x, clone, origin, "main", pin); err != nil {
			return err
		}
		// Only main's commits: a branch's may never reach main, and hosts
		// would build what was never merged.
		onMain, err := fleet.Follows(x, clone, pin, "refs/remotes/origin/main")
		if err != nil {
			return err
		}
		if !onMain {
			return fmt.Errorf("%s is not on fleet-manager's main: pin only commits that are on main", to)
		}
	}
	x.Values["new_pin"] = pin
	if pin == x.Value("old_pin") {
		x.Values["done"] = "true"
		x.UI.OK("the inventory pins " + fleet.Short(pin) + " already: nothing to do")
		return nil
	}
	x.UI.OK("pinning " + fleet.Short(pin))
	return nil
}

// commitLog lists the commits of a range of fleet-manager's clone, newest
// first, one per line.
func commitLog(x *engine.Ctx, rng string) ([]string, error) {
	res, err := engine.Cmd("git", "-C", "{{.fm_clone}}", "log", "--format=%h %ad %s", "--date=short", rng).RunQuiet(x)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// commits is a count of commits in words.
func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

func showCommits(x *engine.Ctx, lines []string) {
	const max = 30
	shown := lines
	if len(shown) > max {
		shown = append(append([]string{}, lines[:max]...), fmt.Sprintf("… and %d more", len(lines)-max))
	}
	x.UI.Text("  " + strings.Join(shown, "\n  "))
}

func seeWhatChanges(x *engine.Ctx) error {
	oldPin, newPin, clone := x.Value("old_pin"), x.Value("new_pin"), x.Value("fm_clone")
	x.Values["pin_count"] = "0"
	_, err := fleet.GitIn(x, clone, "cat-file", "-e", oldPin+"^{commit}")
	switch {
	case err != nil:
		x.UI.Note("the pinned commit is not in fleet-manager's history as fetched")
		if _, err := x.Approve("Pin " + fleet.Short(newPin) + " anyway?"); err != nil {
			return err
		}
	default:
		up, err := fleet.Follows(x, clone, oldPin, newPin)
		if err != nil {
			return err
		}
		if up {
			lines, err := commitLog(x, oldPin+".."+newPin)
			if err != nil {
				return err
			}
			x.Values["pin_count"] = fmt.Sprint(len(lines))
			x.UI.OK(fmt.Sprintf("%s since the pinned %s", commits(len(lines)), fleet.Short(oldPin)))
			showCommits(x, lines)
			break
		}
		down, err := fleet.Follows(x, clone, newPin, oldPin)
		if err != nil {
			return err
		}
		if down {
			lines, err := commitLog(x, newPin+".."+oldPin)
			if err != nil {
				return err
			}
			x.UI.Note(fmt.Sprintf("%s is older than the pinned %s: pinning it takes %s out:", fleet.Short(newPin), fleet.Short(oldPin), commits(len(lines))))
			showCommits(x, lines)
			if _, err := x.Approve("Pin the older commit?"); err != nil {
				return err
			}
			break
		}
		x.UI.Note(fmt.Sprintf("the pinned %s and %s have gone apart: neither follows the other in fleet-manager's history", fleet.Short(oldPin), fleet.Short(newPin)))
		if _, err := x.Approve("Pin " + fleet.Short(newPin) + " anyway?"); err != nil {
			return err
		}
	}
	hosts, err := evalHosts(x)
	if err != nil {
		return err
	}
	data, err := json.Marshal(hosts)
	if err != nil {
		return err
	}
	x.Values["hosts_before"] = string(data)
	return nil
}

// evalHosts is each host's system derivation, by name. An inventory without
// Macs, or without NixOS hosts, has no such attribute: that is no hosts.
func evalHosts(x *engine.Ctx) (map[string]string, error) {
	hosts := map[string]string{}
	for _, kind := range []string{"nixos", "darwin"} {
		res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#"+kind+"Configurations",
			"--apply", "cs: builtins.mapAttrs (_: c: c.config.system.build.toplevel.drvPath) cs").
			ReadOnly().Quiet().OK("evaluated the " + kind + " hosts").Run(x)
		if err != nil {
			if strings.Contains(res.Stderr, "does not provide attribute") {
				continue
			}
			return nil, fmt.Errorf("evaluating the %s hosts: %w: %s", kind, err, strings.TrimSpace(res.Stderr))
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(res.Stdout), &m); err != nil {
			return nil, fmt.Errorf("%sConfigurations: %w", kind, err)
		}
		for h, d := range m {
			hosts[h] = d
		}
	}
	return hosts, nil
}

func lockNewPin(x *engine.Ctx) error {
	_, err := engine.Cmd("nix", "flake", "lock", "--override-input", "fleet-manager", "git+"+fleet.UpstreamURL+"?rev={{.new_pin}}").
		In("{{.flake}}").
		After(func(x *engine.Ctx) error {
			x.Undo("flake.lock", engine.Cmd("git", "-C", "{{.flake}}", "checkout", "--", "flake.lock").OK("put flake.lock back"))
			return nil
		}).OK("flake.lock pins fleet-manager at {{.new_pin}}").Run(x)
	return err
}

func checkHostsAfterPin(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: flake.lock is not changed, so there is nothing new to evaluate")
		x.Values["hosts_changed"] = ""
		return nil
	}
	after, err := evalHosts(x)
	if err != nil {
		return err
	}
	before := map[string]string{}
	if err := json.Unmarshal([]byte(x.Value("hosts_before")), &before); err != nil {
		return err
	}
	seen := map[string]bool{}
	var names []string
	for h := range after {
		seen[h] = true
		names = append(names, h)
	}
	for h := range before {
		if !seen[h] {
			names = append(names, h)
		}
	}
	sort.Strings(names)
	var changed []string
	for _, h := range names {
		switch {
		case after[h] == "":
			x.UI.OK(h + ": is no longer evaluated; it is gone from the inventory")
			changed = append(changed, h)
		case after[h] != before[h]:
			x.UI.OK(h + ": its system changes; it rebuilds after you publish")
			changed = append(changed, h)
		default:
			x.UI.Skip(h + ": stays the same")
		}
	}
	x.Values["hosts_changed"] = strings.Join(changed, ", ")
	return nil
}

func commitPin(x *engine.Ctx) error {
	n, _ := strconv.Atoi(x.Value("pin_count"))
	body := fmt.Sprintf("From %s to %s: %s.", fleet.Short(x.Value("old_pin")), fleet.Short(x.Value("new_pin")), commits(n))
	switch c := x.Value("hosts_changed"); {
	case x.Options.DryRun:
		// The hosts were not evaluated with the new lock.
	case c != "":
		body += " Hosts that change: " + c + "."
	default:
		body += " No host changes."
	}
	x.Values["pin_subject"] = "build: pin fleet-manager at " + fleet.Short(x.Value("new_pin"))
	x.Values["pin_body"] = body
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "commit", "-q", "-m", "{{.pin_subject}}", "-m", "{{.pin_body}}", "--", "flake.lock").
		OK("committed: {{.pin_subject}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	// The change is part of a commit now; a stop leaves the commit, unpushed.
	x.Done("flake.lock")
	x.Kept("push pin", "the commit “{{.pin_subject}}” in {{.flake}}, not pushed yet (git -C {{.flake}} push origin HEAD:refs/heads/main)")
	return nil
}

func pushPin(x *engine.Ctx) error {
	commit := ""
	if !x.Options.DryRun {
		var err error
		if commit, err = fleet.CommitOf(x, x.Value("flake"), "HEAD"); err != nil {
			return err
		}
	}
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "push", "origin", "HEAD:refs/heads/main").OK("pushed").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	x.Kept("push pin", "")
	// What the tool pushed is what you trust on main now.
	return fleet.WriteTrusted(x.Value("inv_origin"), "main", commit)
}
