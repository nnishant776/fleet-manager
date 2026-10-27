package flows

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// cloneSync starts every command that takes a checkout or a repository (see
// flows.go), ahead of the keys comparison: when the repository was given by
// name, the command works in the tool's own clone of it, which is brought up
// to date here.
var cloneSync = engine.Step{
	Title: "Bring the tool's clone up to date",
	Why: "You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken " +
		"only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.",
	Required: true,
	When: func(x *engine.Ctx) (bool, string) {
		if x.Value("flake_name") != "" || x.Value("repo_name") != "" {
			return true, ""
		}
		return false, "a checkout or flake given by path"
	},
	Do: engine.Func(func(x *engine.Ctx) error {
		for _, in := range []string{"flake", "repo"} {
			name := x.Value(in + "_name")
			if name == "" {
				continue
			}
			dir := x.Value(in)
			if !fileExists(dir) {
				return fmt.Errorf("the clone of %s is not in %s: run fleet repo add again with its address", name, dir)
			}
			x.UI.OK(name + " is the tool's clone in " + dir)
			if err := fleet.SyncClone(x, dir); err != nil {
				return err
			}
		}
		return nil
	}),
}

// nameFromAddress is the last part of a repository's id, as a name for it.
func nameFromAddress(x *engine.Ctx) (string, error) {
	id, err := fleet.RepoID(x.Value("url"))
	if err != nil {
		return "", err
	}
	return strings.ToLower(id[strings.LastIndex(id, "/")+1:]), nil
}

// RepoAdd makes the tools' clone of a repository and names it.
var RepoAdd = &engine.Flow{
	Path:  []string{"repo", "add"},
	Title: "Keep a clone of a repository for the tools",
	Short: "Clone a repository for the tools to work in, check its keys and main, and give it a name",
	Long: "The tools keep their own clone of each repository, under ~/.local/share/fleet/repos\n" +
		"(XDG_DATA_HOME), which nobody edits. fleet repo add clones it, keeps your copy of the\n" +
		"keys that sign its commits (the repository's own fleet/allowed_signers, or those of\n" +
		"the repository named by --signers-from, for one without), checks main and the hosts'\n" +
		"branches, and names the clone. After that --flake NAME and --repo NAME mean the clone,\n" +
		"which every command fetches first, taking main's newest commit only if a key you trust\n" +
		"signed it and it follows the commit you trusted last.",
	Example: "  fleet repo add git@github.com:example-org/fleet-manager.git\n" +
		"  fleet repo add git@github.com:example-org/inventory.git\n" +
		"  fleet repo add git@github.com:example-org/secrets.git --signers-from inventory",
	Args: []string{"url"},
	Inputs: []engine.Input{
		{Name: "url", Prompt: "Address of the repository to add",
			Help: "Your repositories are added one at a time: fleet-manager, your inventory, then your secrets repository. " +
				"Give the address git clones it from, SSH or https (a local one needs file://); fleet repo list shows the ones added already"},
		{Name: "name", Prompt: "Name for it", Help: "What --flake and --repo take for it; the last part of its address by default",
			Optional: true, Pattern: fleet.NamePattern, Default: nameFromAddress},
		{Name: "signers-from", Prompt: "Repository whose keys sign it",
			Help:     "For a repository without fleet/allowed_signers, such as the secrets repository: the name of the repository whose keys sign its commits",
			Optional: true},
		{Name: "signing-key", Prompt: "Signing key", Help: "The SSH private key that signs commits made in the clone; made by fleet setup",
			Default: func(*engine.Ctx) (string, error) {
				home, err := os.UserHomeDir()
				return filepath.Join(home, ".ssh", "fleet_manager_config_signing_key"), err
			}},
	},
	Needs: []string{"git"},
	Steps: []engine.Step{
		{
			Title: "Clone {{.url}}",
			Why:   "The tools work in a clone of their own, kept under ~/.local/share/fleet/repos, so that what they read is not a checkout you edit. It is left on main's newest commit, detached.",
			Do:    engine.Func(repoClone),
		},
		{
			Title: "Trust its keys",
			Why: "Commits are checked against your own copy of the keys that sign them, not against a file a pushed commit could change. " +
				"A repository with a fleet/allowed_signers gives them; one without, such as the secrets repository, is signed with the keys of another, which --signers-from names, or which you choose when asked.",
			Do: engine.Func(repoTrustKeys),
		},
		{
			Title: "Check main and trust it",
			Why:   "Main's newest commit must be signed by a key you trust. It becomes the first commit you trust for main, and later ones are accepted only when they follow it.",
			Do:    engine.Func(repoTrustMain),
		},
		{
			Title: "Record the hosts' branches",
			Why: "Each host has a branch, host/NAME. The same is done for the ones signed by a key you trust, so that an old commit served again is noticed. " +
				"The branches of users, user/NAME, are signed by their users, and are recorded the first time a command checks them.",
			Do: engine.Func(repoTrustHosts),
		},
		{
			Title: "Sign the commits made in it",
			Why:   "Hosts accept only signed commits, so the clone is set to sign the ones made in it with your signing key, as fleet setup does for your checkouts.",
			When: func(x *engine.Ctx) (bool, string) {
				if !fileExists(x.Value("signing-key") + ".pub") {
					return false, x.Value("signing-key") + ".pub does not exist: run fleet setup first"
				}
				return true, ""
			},
			Do: engine.Func(repoSigning),
		},
		{
			Title: "Install the secrets repository's commit check",
			Why: "Its pre-commit hook refuses anything under hosts/ or users/ that is not ciphertext. Git runs a hook as you, with your agent at hand, " +
				"so what is installed is a copy you have read, not the repository's own file, which anyone who can push could change.",
			When: func(x *engine.Ctx) (bool, string) {
				if noClone(x) {
					return false, "dry run: nothing is cloned yet"
				}
				return hookNotInstalled(x)
			},
			Do: engine.Func(installHook),
		},
		{
			Title: "Name it {{.name}}",
			Why:   "So that --flake NAME or --repo NAME means this clone. A name names one repository only.",
			Do: engine.Func(func(x *engine.Ctx) error {
				if x.Options.DryRun {
					x.UI.Skip("dry run: " + x.Value("name") + " would be " + x.Value("repo_id"))
					return nil
				}
				if err := fleet.SetName(x.Value("name"), x.Value("repo_id")); err != nil {
					return err
				}
				x.UI.OK(x.Value("name") + " is " + x.Value("repo_id") + ", cloned in " + x.Value("clone_path"))
				return nil
			}),
		},
		{
			Title: "Next",
			Why:   "How to use it.",
			Do: engine.Show("Use the name wherever a command takes --flake or --repo: fleet inventory publish --flake {{.name}}\n" +
				"Edit in a checkout of your own; never edit the tool's clone by hand."),
		},
	},
}

// noClone is true in a dry run that has no clone to look at yet.
func noClone(x *engine.Ctx) bool {
	return x.Options.DryRun && !fileExists(x.Value("clone_path"))
}

func repoClone(x *engine.Ctx) error {
	url := x.Value("url")
	id, err := fleet.RepoID(url)
	if err != nil {
		return err
	}
	path, err := fleet.ClonePath(url)
	if err != nil {
		return err
	}
	x.Values["repo_id"], x.Values["clone_path"] = id, path
	x.Values["flake"], x.Values["repo"] = path, path
	if fileExists(path) {
		origin, err := fleet.OriginOf(x, path)
		if err != nil {
			return err
		}
		have, err := fleet.RepoID(origin)
		if err != nil || have != id {
			return fmt.Errorf("%s exists, and is a clone of %s, not of %s", path, origin, url)
		}
		// A clone the tools left is detached; one still on a branch is where
		// git clone left it, before the checkout below, which did not happen.
		if _, err := engine.Silently(x, nil, "git", "-C", path, "symbolic-ref", "-q", "HEAD"); err != nil {
			x.UI.Skip("cloned already, in " + path)
			return nil
		}
		x.UI.OK("cloned already, in " + path + ", but not checked out")
	} else {
		if !x.Options.DryRun {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
		}
		if _, err := engine.Cmd("git", "clone", "-q", "--no-checkout", url, path).OK("cloned into " + path).Run(x); err != nil {
			return err
		}
	}
	_, err = engine.Cmd("git", "-C", path, "checkout", "-q", "--detach", "refs/remotes/origin/main").
		OK("the clone is on origin's main").Run(x)
	return err
}

func repoTrustKeys(x *engine.Ctx) error {
	path := x.Value("clone_path")
	if noClone(x) {
		x.UI.Skip("dry run: nothing is cloned yet")
		return nil
	}
	if fileExists(filepath.Join(path, "fleet", "allowed_signers")) {
		if err := readCheckoutSigners(x); err != nil {
			return err
		}
		if err := keepSignersCopy(x); err != nil {
			return err
		}
		return pointGitAtSignersCopy(x)
	}
	names, err := fleet.ReadNames()
	if err != nil {
		return err
	}
	from := x.Value("signers-from")
	if from == "" {
		if from, err = chooseSignersFrom(x, names); err != nil {
			return err
		}
	}
	id, ok := names[from]
	if !ok {
		return fmt.Errorf("%q is not the name of a repository: fleet repo list shows the names, fleet repo add makes one", from)
	}
	other, err := fleet.ClonePathOfID(id)
	if err != nil {
		return err
	}
	origin, err := fleet.OriginOf(x, other)
	if err != nil {
		return err
	}
	copyPath, err := fleet.SignersCopy(origin)
	if err != nil {
		return err
	}
	if !fileExists(copyPath) {
		return fmt.Errorf("you trust no keys for %s yet: run fleet repo add for it first, with its own fleet/allowed_signers", id)
	}
	x.UI.OK("its commits are checked against your copy of the keys of " + from + " (" + id + ")")
	return setGitConfig(x, path, "gpg.ssh.allowedSignersFile", copyPath)
}

// signersFromWhy says which repository's keys a repository without its own
// fleet/allowed_signers takes.
const signersFromWhy = "Hosts check the secrets repository's commits against the keys of the configuration they build: " +
	"name your inventory, or fleet-manager if its own hosts use these secrets."

// chooseSignersFrom asks, on a terminal, which named repository's keys sign
// the commits of a repository that has no fleet/allowed_signers, offering
// those whose keys you keep a copy of; elsewhere it says to give
// --signers-from, and which names there are.
func chooseSignersFrom(x *engine.Ctx, names map[string]string) (string, error) {
	var choices []string
	for n, id := range names {
		if id == x.Value("repo_id") {
			continue
		}
		clone, err := fleet.ClonePathOfID(id)
		if err != nil || !fileExists(clone) {
			continue
		}
		origin, err := fleet.OriginOf(x, clone)
		if err != nil {
			continue
		}
		if p, err := fleet.SignersCopy(origin); err == nil && fileExists(p) {
			choices = append(choices, n)
		}
	}
	sort.Strings(choices)
	id := x.Value("repo_id")
	if len(choices) == 0 {
		return "", fmt.Errorf("%s has no fleet/allowed_signers, so its commits are checked against the keys of another repository, "+
			"and none is added yet: add that one first (fleet repo add ADDRESS), then this one with --signers-from NAME. %s", id, signersFromWhy)
	}
	if !x.UI.Interactive() {
		return "", fmt.Errorf("%s has no fleet/allowed_signers, so its commits are checked against the keys of another repository: "+
			"give --signers-from NAME, one of: %s. %s", id, strings.Join(choices, ", "), signersFromWhy)
	}
	from, err := x.UI.Choose("Whose keys sign the commits of "+id+"?", "It has no fleet/allowed_signers of its own. "+signersFromWhy, choices)
	if err != nil {
		return "", err
	}
	x.Values["signers-from"] = from
	return from, nil
}

func verifyCommit(x *engine.Ctx, dir, ref string) error {
	_, err := engine.Cmd("git", "-C", dir, "verify-commit", ref).ReadOnly().Quiet().OK(ref + " is signed by a key you trust").Run(x)
	return err
}

func repoTrustMain(x *engine.Ctx) error {
	path := x.Value("clone_path")
	if noClone(x) {
		x.UI.Skip("dry run: nothing is cloned yet")
		return nil
	}
	if err := verifyCommit(x, path, "refs/remotes/origin/main"); err != nil {
		return fmt.Errorf("origin's main is not signed by a key you trust: git checks it against the clone's gpg.ssh.allowedSignersFile. "+
			"Look at it with git -C %s log --show-signature -1 origin/main (%w)", path, err)
	}
	origin, err := fleet.OriginOf(x, path)
	if err != nil {
		return err
	}
	return fleet.CheckTrusted(x, path, origin, "main", "refs/remotes/origin/main")
}

func repoTrustHosts(x *engine.Ctx) error {
	path := x.Value("clone_path")
	if noClone(x) {
		x.UI.Skip("dry run: nothing is cloned yet")
		return nil
	}
	origin, err := fleet.OriginOf(x, path)
	if err != nil {
		return err
	}
	out, err := engine.Silently(x, nil, "git", "-C", path, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/host/")
	if err != nil {
		return err
	}
	n := 0
	for _, ref := range strings.Fields(out) {
		branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
		if err := verifyCommit(x, path, ref); err != nil {
			x.UI.Note(branch + " is not signed by a key you trust, so it is not recorded: " + err.Error())
			continue
		}
		if err := fleet.CheckTrusted(x, path, origin, branch, ref); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		x.UI.Skip("no host branches to record")
	}
	return nil
}

func repoSigning(x *engine.Ctx) error {
	if noClone(x) {
		x.UI.Skip("dry run: nothing is cloned yet")
		return nil
	}
	path := x.Value("clone_path")
	for _, kv := range [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingKey", x.Value("signing-key") + ".pub"},
		{"commit.gpgSign", "true"},
		{"tag.gpgSign", "true"},
	} {
		if err := setGitConfig(x, path, kv[0], kv[1]); err != nil {
			return err
		}
	}
	x.UI.OK(path + " signs every commit")
	return nil
}

// RepoList shows the names, and what is kept for each repository.
var RepoList = &engine.Flow{
	Path:  []string{"repo", "list"},
	Title: "The tools' clones of your repositories",
	Short: "List the names of the tools' clones, and the commits you trust in each",
	Long: "Each name that fleet repo add made, with its repository, address and clone, the\n" +
		"commit the clone stands at, and the commit you trust for each branch. Repositories\n" +
		"that have trusted commits or a copy of their keys but no name are listed apart.",
	Example: "  fleet repo list",
	Steps: []engine.Step{{
		Title: "List them",
		Why:   "What is kept in ~/.local/state/fleet (names, trusted commits, keys) and ~/.local/share/fleet (the clones).",
		Do:    engine.Func(listRepos),
	}},
}

func shortHash(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func listRepos(x *engine.Ctx) error {
	names, err := fleet.ReadNames()
	if err != nil {
		return err
	}
	byName := make([]string, 0, len(names))
	named := map[string]bool{}
	for n, id := range names {
		byName = append(byName, n)
		named[id] = true
	}
	sort.Strings(byName)
	var lines []string
	for _, n := range byName {
		id := names[n]
		clone, err := fleet.ClonePathOfID(id)
		if err != nil {
			return err
		}
		address, at := "(no clone)", ""
		if fileExists(clone) {
			if o, err := fleet.OriginOf(x, clone); err == nil {
				address = o
			}
			if h, err := engine.Silently(x, nil, "git", "-C", clone, "rev-parse", "HEAD"); err == nil && h != "" {
				at = shortHash(h)
			}
		}
		lines = append(lines, n, "  repository  "+id, "  address     "+address, "  clone       "+clone)
		if at != "" {
			lines = append(lines, "  clone is at "+at)
		}
		recs, err := fleet.Records(id)
		if err != nil {
			return err
		}
		lines = append(lines, trustedLines(recs)...)
		lines = append(lines, "")
	}
	if len(names) == 0 {
		lines = append(lines, "No names yet: fleet repo add ADDRESS makes one.", "")
	}
	ids, err := fleet.KnownIDs()
	if err != nil {
		return err
	}
	var rest []string
	for _, id := range ids {
		if !named[id] {
			rest = append(rest, "  "+id)
		}
	}
	if len(rest) > 0 {
		lines = append(lines, "Without a name", "")
		lines = append(lines, rest...)
	}
	x.UI.Text(strings.TrimRight(strings.Join(lines, "\n"), "\n"))
	return nil
}

func trustedLines(recs map[string]string) []string {
	var branches []string
	for b := range recs {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	var lines []string
	for i, b := range branches {
		label := "  trusted     "
		if i > 0 {
			label = "              "
		}
		lines = append(lines, label+b+" "+shortHash(recs[b]))
	}
	return lines
}
