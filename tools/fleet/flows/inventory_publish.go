package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// InventoryPublish cuts each host's own files from a private inventory's
// main into the branch that host fetches, host/<name> (lib.mkFleet's
// hostBranches), so that no host gets the others' definitions. A branch has
// a history of its own: each publish adds one signed commit on top of the
// branch's last, so hosts' protection against older commits holds per
// branch. Only a main whose newest commit you trust is published, and only
// on top of branch commits you trust.
var InventoryPublish = &engine.Flow{
	Path:  []string{"inventory", "publish"},
	Title: "Publish each host's branch from the inventory",
	Short: "Cut each host's own files from main into its branch, host/<name>, signed, and push them",
	Long: "For an inventory whose lib.mkFleet sets hostBranches = true. Each host fetches only\n" +
		"its branch, host/<name>, which holds flake.nix, flake.lock, fleet/, the host's\n" +
		"folder, its users' folders and the paths listed as shared, and nothing of the\n" +
		"other hosts. fleet checks that main's newest commit is signed by a key you trust\n" +
		"and pushed, cuts each branch from it without touching the checkout, evaluates it,\n" +
		"and adds a signed commit to every branch whose files changed. Then it pushes them,\n" +
		"once you approve. A branch whose newest commit no key you trust signed is not\n" +
		"published on.",
	Example: "  fleet inventory publish --flake ~/Projects/inventory\n" +
		"  fleet inventory publish nas1 --flake ~/Projects/inventory",
	Args: []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Hosts to publish", Help: "A host to publish; give --host more than once for several; all of them by default",
			Many: true, Optional: true},
		{Name: "no-check", Prompt: "Skip evaluating each branch?", Help: "Do not evaluate each branch before publishing it; for tests without Nix", Bool: true},
	},
	Needs: []string{"git", "nix", "tar"},
	Steps: []engine.Step{
		{
			Title: "Check main's newest commit",
			Why:   "Every branch is cut from it, so it must be signed by a key you trust and pushed, and the checkout clean.",
			Do:    engine.Func(checkPublishSource),
		},
		{
			Title: "Cut each host's branch",
			Why:   "With the host's own files only. A branch whose files did not change gets no commit.",
			Do:    engine.Func(cutHostBranches),
		},
		{
			Title: "Push the branches",
			Why:   "Hosts fetch their branch on their next sync. A push that is not a fast-forward is refused: then someone else changed the branch.",
			Do:    engine.Func(pushHostBranches),
		},
	},
}

// layout is lib.mkFleet's fleetLayout output.
type layout struct {
	HostBranches bool     `json:"hostBranches"`
	Hosts        string   `json:"hosts"`
	Users        *string  `json:"users"`
	Shared       []string `json:"shared"`
}

func publishGit(args ...string) *engine.Command {
	return engine.Cmd("git", append([]string{"-C", "{{.flake}}"}, args...)...).ReadOnly().Quiet()
}

func checkPublishSource(x *engine.Ctx) error {
	flake := x.Value("flake")
	if _, err := os.Stat(filepath.Join(flake, ".git")); err != nil {
		return fmt.Errorf("%s is not a git checkout of the inventory", flake)
	}
	if _, err := publishGit("remote", "get-url", "origin").Capture("origin").OK("the checkout fetches {{.origin}}").Run(x); err != nil {
		return err
	}
	signers, err := trustedSignersFor(x, x.Value("origin"), flake)
	if err != nil {
		return err
	}
	x.Values["signers"] = signers
	steps := []*engine.Command{
		publishGit("status", "--porcelain", "--untracked-files=no").Expect(func(r engine.Result) error {
			if strings.TrimSpace(r.Stdout) != "" {
				return errors.New("the checkout has uncommitted changes: branches are cut from a commit, so commit or discard them")
			}
			return nil
		}).OK("no uncommitted changes"),
		publishGit("rev-parse", "HEAD").Capture("main_rev").OK("its newest commit is {{.main_rev}}"),
		publishGit("-c", "gpg.ssh.allowedSignersFile={{.signers}}", "verify-commit", "HEAD").
			Explain(func(engine.Result) string {
				return "the newest commit, {{.main_rev}}, is not signed by a key you trust ({{.signers}})"
			}).OK("{{.main_rev}} is signed by a key you trust"),
		publishGit("branch", "-r", "--contains", "HEAD").Expect(func(r engine.Result) error {
			if strings.TrimSpace(r.Stdout) == "" {
				return errors.New("the newest commit is on no branch of the remote: push it first, so that what hosts get is on main too")
			}
			return nil
		}).OK("pushed"),
		engine.Cmd("nix", "eval", "--json", "{{.flake}}#fleetLayout").ReadOnly().Quiet().Capture("layout").OK("read the inventory's layout"),
		// Every host's accounts, whose folders its branch gets.
		engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations",
			"--apply", "cs: builtins.mapAttrs (_: c: builtins.attrNames c.config.conf.users.accounts) cs").
			ReadOnly().Quiet().Capture("nixos_accounts").OK("read the hosts' accounts"),
		engine.Cmd("nix", "eval", "--json", "{{.flake}}#darwinConfigurations",
			"--apply", "cs: builtins.mapAttrs (_: c: builtins.attrNames c.config.conf.users.accounts) cs").
			ReadOnly().Quiet().Capture("darwin_accounts").OK("read the Macs' accounts"),
		// The branches as the remote has them, to build on.
		publishGit("fetch", "-q", "origin", "+refs/heads/host/*:refs/remotes/origin/host/*").OK("fetched the hosts' branches"),
	}
	for _, c := range steps {
		if _, err := c.Run(x); err != nil {
			return err
		}
	}
	// Signed is not enough: an old main, served again, is signed too.
	if err := fleet.CheckTrusted(x, flake, x.Value("origin"), "main", x.Value("main_rev")); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	var l layout
	if err := json.Unmarshal([]byte(x.Value("layout")), &l); err != nil {
		return fmt.Errorf("fleetLayout: %w", err)
	}
	if !l.HostBranches {
		return errors.New("the inventory's lib.mkFleet does not set hostBranches = true, so its hosts fetch main, not branches of their own")
	}
	return nil
}

// hostAccounts is every host the inventory builds, with its accounts.
func hostAccounts(x *engine.Ctx) (map[string][]string, map[string]string, error) {
	accounts, kind := map[string][]string{}, map[string]string{}
	for _, k := range []string{"nixos", "darwin"} {
		var m map[string][]string
		if err := json.Unmarshal([]byte(x.Value(k+"_accounts")), &m); err != nil {
			return nil, nil, fmt.Errorf("%sConfigurations: %w", k, err)
		}
		for h, a := range m {
			accounts[h], kind[h] = a, k
		}
	}
	return accounts, kind, nil
}

// sliceTree builds the tree of host's branch from commit rev with a scratch
// index, leaving the checkout as it is, and returns it.
func sliceTree(x *engine.Ctx, rev, host string, l layout, accounts []string) (string, error) {
	paths := []string{"flake.nix", "flake.lock", "fleet", l.Hosts + "/" + host}
	if l.Users != nil {
		for _, u := range accounts {
			p := *l.Users + "/" + u
			if _, err := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "cat-file", "-e", rev+":"+p); err == nil {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range l.Shared {
		if _, err := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "cat-file", "-e", rev+":"+p); err != nil {
			return "", fmt.Errorf("%s, listed as shared, is not in %s", p, rev)
		}
		paths = append(paths, p)
	}
	listing, err := engine.Silently(x, nil, "git", append([]string{"-C", x.Value("flake"), "ls-tree", "-r", rev, "--"}, paths...)...)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "fleet-publish-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	x.Values["slice_listing"] = listing + "\n"
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "update-index", "--index-info").Env(index...).
		StdinFrom("slice_listing").StdinNote("the listing of " + host + "'s files").ReadOnly().Quiet().OK("listed " + host + "'s files").Run(x); err != nil {
		return "", err
	}
	tree, err := engine.Silently(x, index, "git", "-C", x.Value("flake"), "write-tree")
	if err != nil {
		return "", err
	}
	return tree, nil
}

// published is the push fleet makes: one commit per branch whose files
// changed.
type published struct{ host, commit string }

func cutHostBranches(x *engine.Ctx) error {
	if x.Options.DryRun && x.Value("layout") == "" {
		x.UI.Skip("dry run: nothing read to cut branches from")
		return nil
	}
	var l layout
	if err := json.Unmarshal([]byte(x.Value("layout")), &l); err != nil {
		return err
	}
	accounts, kind, err := hostAccounts(x)
	if err != nil {
		return err
	}
	hosts := strings.Fields(x.Value("host"))
	if len(hosts) == 0 {
		for h := range accounts {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
	}
	var pushes []published
	for _, h := range hosts {
		if _, ok := accounts[h]; !ok {
			return fmt.Errorf("the inventory has no host %s", h)
		}
		c, err := cutHostBranch(x, h, kind[h], l, accounts[h])
		if err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
		if c != "" {
			pushes = append(pushes, published{h, c})
		}
	}
	var lines []string
	for _, p := range pushes {
		lines = append(lines, p.commit+":refs/heads/host/"+p.host)
	}
	x.Values["publish_refs"] = strings.Join(lines, "\n")
	return nil
}

// cutHostBranch makes host's next branch commit, or returns "" when its
// files did not change.
func cutHostBranch(x *engine.Ctx, host, kind string, l layout, accounts []string) (string, error) {
	tree, err := sliceTree(x, x.Value("main_rev"), host, l, accounts)
	if err != nil {
		return "", err
	}
	x.Values["slice_host"], x.Values["slice_tree"] = host, tree
	parent, err := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "rev-parse", "-q", "--verify", "refs/remotes/origin/host/"+host+"^{commit}")
	if err == nil && parent != "" {
		x.Values["slice_parent"] = parent
		// A commit someone else pushed to the branch is not built on.
		if _, err := publishGit("-c", "gpg.ssh.allowedSignersFile={{.signers}}", "verify-commit", "{{.slice_parent}}").
			Explain(func(engine.Result) string {
				return "the newest commit of host/{{.slice_host}}, {{.slice_parent}}, is not signed by a key you trust: " +
					"someone else pushed it; look at it with git log --show-signature before publishing"
			}).Run(x); err != nil {
			return "", err
		}
		if err := fleet.CheckTrusted(x, x.Value("flake"), x.Value("origin"), "host/"+host, parent); err != nil {
			return "", err
		}
		if t, _ := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "rev-parse", parent+"^{tree}"); t == tree {
			x.UI.Skip("host/" + host + ": unchanged")
			return "", nil
		}
	} else {
		x.Values["slice_parent"] = ""
	}
	if x.Value("no-check") != "true" {
		if err := checkSlice(x, host, kind, tree); err != nil {
			return "", err
		}
	}
	args := []string{"-C", "{{.flake}}", "commit-tree", "{{.slice_tree}}", "-S",
		"-m", "publish: " + host, "-m", "Cut from main at {{.main_rev}}."}
	if x.Value("slice_parent") != "" {
		args = append(args, "-p", "{{.slice_parent}}")
	}
	res, err := engine.Cmd("git", args...).Quiet().OK("signed a commit for host/" + host).Run(x)
	if err != nil || x.Options.DryRun {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// checkSlice evaluates the host from its branch's files alone, exported to a
// scratch folder, so a file the host needs from outside its folder is caught.
func checkSlice(x *engine.Ctx, host, kind, tree string) error {
	dir, err := os.MkdirTemp("", "fleet-slice-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	x.Values["slice_dir"] = dir
	if _, err := engine.Cmd("sh", "-c", `git -C "$1" archive "$2" | tar -x -C "$3"`, "sh", "{{.flake}}", "{{.slice_tree}}", "{{.slice_dir}}").
		ReadOnly().Quiet().OK("exported " + host + "'s files to evaluate them").Run(x); err != nil {
		return err
	}
	_, err = engine.Cmd("nix", "eval", "--raw", "path:{{.slice_dir}}#"+kind+"Configurations."+host+".config.system.build.toplevel.drvPath").
		ReadOnly().Quiet().
		Explain(func(engine.Result) string {
			return host + " does not build from its branch's files alone: does it use a file outside its folder? List such paths in lib.mkFleet's shared"
		}).OK(host + " builds from its branch's files alone").Run(x)
	return err
}

func pushHostBranches(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing committed or pushed")
		return nil
	}
	refs := strings.Fields(x.Value("publish_refs"))
	if len(refs) == 0 {
		x.UI.OK("every branch is up to date")
		return nil
	}
	x.UI.Text("  " + strings.Join(refs, "\n  "))
	ok, err := x.Approve(fmt.Sprintf("Push %d branch(es)?", len(refs)))
	if !ok || err != nil {
		return err
	}
	_, err = engine.Cmd("git", append([]string{"-C", "{{.flake}}", "push", "--atomic", "origin"}, refs...)...).OK("pushed").Run(x)
	if err != nil {
		return err
	}
	// What was pushed is what you trust on those branches now.
	for _, r := range refs {
		commit, ref, _ := strings.Cut(r, ":")
		if err := fleet.WriteTrusted(x.Value("origin"), strings.TrimPrefix(ref, "refs/heads/"), commit); err != nil {
			return err
		}
	}
	return nil
}
