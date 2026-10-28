package flows

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// InstallPrep checks, on the maintainer's machine, the two commits an install
// takes, and prints the line that installs exactly those on the installer:
// the configuration commit, this checkout's newest, and the fleet-manager
// commit the installer comes from, the one its flake.lock pins, or the same
// commit when this is fleet-manager itself. Both must be signed by a key in
// the checkout's fleet/allowed_signers. The line fetches bootstrap by that
// commit and checks its SHA-256, worked out here, before running it.
var InstallPrep = &engine.Flow{
	Path:  []string{"install", "prep"},
	Title: "Prepare a host's install from checked commits",
	Short: "Check the commits a host's install takes, and print the line to run on the installer",
	Long: "Run on your machine, in the configuration checkout, before installing a host.\n" +
		"fleet checks that the checkout's newest commit is signed by a key in its\n" +
		"fleet/allowed_signers and pushed, and so is the fleet-manager commit its\n" +
		"flake.lock pins, which the installer comes from. It then prints one line to run\n" +
		"on the machine booted from the NixOS installer: it fetches bootstrap by that\n" +
		"commit, checks it against a checksum worked out here, and installs exactly\n" +
		"those commits, so that a push to main since cannot change what gets installed.",
	Example: "  fleet install prep nas1 --flake ~/Projects/inventory",
	Args:    []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Which host?", Help: "A host of the configuration (hosts/<name>)",
			Pattern: secrets.NamePattern, Choices: fleet.Hosts},
	},
	Needs: []string{"git", "nix"},
	Steps: []engine.Step{
		{
			Title: "Check the configuration commit",
			Why:   "It is what gets installed: the checkout's newest commit, which must be signed by a key in its fleet/allowed_signers, and pushed, since the installer fetches it.",
			Do:    engine.Func(checkConfigCommit),
		},
		{
			Title: "Check the fleet-manager commit the installer comes from",
			Why:   "bootstrap, fleet and os-install come from fleet-manager. It is the commit the configuration's flake.lock pins, fetched with git and checked against the same signers file.",
			Do:    engine.Func(checkInstallerCommit),
		},
		{
			Title: "Run this on the installer",
			Why: "On the machine booted from the NixOS installer. Typing it is error-prone: set a password there with passwd, then ssh nixos@<its address> from here and paste it. " +
				"Before you answer ssh's question about the host key, compare the fingerprint it shows with the one the installer's own screen shows " +
				"(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub there): something else on the network could pose as it, and would get the disk passphrase you type.",
			Do: engine.Func(printInstallLine),
		},
	},
}

func checkConfigCommit(x *engine.Ctx) error {
	flake := x.Value("flake")
	if st, err := os.Stat(filepath.Join(flake, ".git")); err != nil || st == nil {
		return fmt.Errorf("%s is not a git checkout: the commits are checked in a checkout of the configuration", flake)
	}
	g := func(args ...string) *engine.Command {
		return engine.Cmd("git", append([]string{"-C", "{{.flake}}"}, args...)...).ReadOnly().Quiet()
	}
	// The repository first: its commits are checked against your copy of its
	// keys, never against the checkout's own fleet/allowed_signers, which a
	// pushed commit could have changed.
	if _, err := g("remote", "get-url", "origin").Capture("origin").OK("the checkout fetches {{.origin}}").Run(x); err != nil {
		return err
	}
	signers, err := trustedSignersFor(x, x.Value("origin"), flake)
	if err != nil {
		return err
	}
	x.Values["signers"] = signers
	steps := []*engine.Command{
		g("status", "--porcelain", "--untracked-files=no").Expect(func(r engine.Result) error {
			if strings.TrimSpace(r.Stdout) != "" {
				return errors.New("the checkout has uncommitted changes: what is installed is a commit, so commit or discard them")
			}
			return nil
		}).OK("no uncommitted changes"),
		g("rev-parse", "HEAD").Capture("inv").OK("its newest commit is {{.inv}}"),
		g("-c", "gpg.ssh.allowedSignersFile={{.signers}}", "verify-commit", "HEAD").
			Explain(func(engine.Result) string {
				return "the newest commit, {{.inv}}, is not signed by a key you trust ({{.signers}})"
			}).OK("{{.inv}} is signed by a key you trust"),
		g("branch", "-r", "--contains", "HEAD").Expect(func(r engine.Result) error {
			if strings.TrimSpace(r.Stdout) == "" {
				return errors.New("the newest commit is on no branch of the remote: push it, since the installer fetches it")
			}
			return nil
		}).OK("pushed"),
		// Empty for a host that syncs from no repository, as the example and
		// template hosts.
		engine.Cmd("nix", "eval", "--raw", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.fleet.repo.url",
			"--apply", `u: if u == null then "" else u`).
			ReadOnly().Quiet().Capture("config_url").OK("read where {{.host}} syncs from"),
		engine.Cmd("nix", "eval", "--raw", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.fleet.repo.ref").
			ReadOnly().Quiet().Capture("config_ref").OK("{{.host}} tracks {{.config_ref}}"),
	}
	for _, c := range steps {
		if _, err := c.Run(x); err != nil {
			return err
		}
	}
	if x.Value("config_ref") == "" {
		x.Values["config_ref"] = "main"
	}
	x.Values["main_rev"] = x.Value("inv")
	// With lib.mkFleet's hostBranches, the host installs from its own branch,
	// which fleet inventory publish cut from this commit.
	var l layout
	if out, err := engine.Silently(x, nil, "nix", "eval", "--json", x.Value("flake")+"#fleetLayout"); err == nil {
		_ = json.Unmarshal([]byte(out), &l)
	}
	hostBranch := l.HostBranches && x.Value("config_ref") == "host/"+x.Value("host")
	// Signed is not enough: an old commit, served again, is signed too. The
	// commit is on the branch the host tracks, or on main when the host has a
	// branch of its own, which publish cut from it.
	trustBranch := x.Value("config_ref")
	if hostBranch {
		trustBranch = "main"
	}
	if err := fleet.CheckTrusted(x, flake, x.Value("origin"), trustBranch, x.Value("inv")); err != nil {
		return err
	}
	if hostBranch {
		return checkHostBranch(x, l)
	}
	// Otherwise this commit, which has to be on the branch the host tracks:
	// the installer clones that branch alone.
	if _, err := g("fetch", "-q", "origin", "+refs/heads/{{.config_ref}}:refs/remotes/origin/{{.config_ref}}").
		OK("fetched {{.config_ref}}").Run(x); err != nil {
		return err
	}
	_, err = g("merge-base", "--is-ancestor", "HEAD", "refs/remotes/origin/{{.config_ref}}").
		Explain(func(engine.Result) string {
			return "the newest commit, {{.inv}}, is not on {{.config_ref}}, the branch {{.host}} tracks: push it there"
		}).OK("{{.inv}} is on {{.config_ref}}").Run(x)
	return err
}

// checkHostBranch takes the commit to install from the host's own branch:
// signed by a key you trust, and cut from the checkout's newest commit, the
// one checked above, so that what is installed is what main says.
func checkHostBranch(x *engine.Ctx, l layout) error {
	steps := []*engine.Command{
		publishGit("fetch", "-q", "origin", "+refs/heads/{{.config_ref}}:refs/remotes/origin/{{.config_ref}}").
			Explain(func(engine.Result) string {
				return "{{.config_ref}} could not be fetched: is it published? fleet inventory publish {{.host}} makes it"
			}).OK("fetched {{.config_ref}}"),
		publishGit("rev-parse", "refs/remotes/origin/{{.config_ref}}^{commit}").Capture("branch_rev").OK("its newest commit is {{.branch_rev}}"),
		publishGit("-c", "gpg.ssh.allowedSignersFile={{.signers}}", "verify-commit", "{{.branch_rev}}").
			Explain(func(engine.Result) string {
				return "the newest commit of {{.config_ref}}, {{.branch_rev}}, is not signed by a key you trust ({{.signers}})"
			}).OK("{{.branch_rev}} is signed by a key you trust"),
		engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.users.accounts", "--apply", "builtins.attrNames").
			ReadOnly().Quiet().Capture("host_accounts").OK("read {{.host}}'s accounts"),
	}
	for _, c := range steps {
		if _, err := c.Run(x); err != nil {
			return err
		}
	}
	if err := fleet.CheckTrusted(x, x.Value("flake"), x.Value("origin"), x.Value("config_ref"), x.Value("branch_rev")); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	var accounts []string
	if err := json.Unmarshal([]byte(x.Value("host_accounts")), &accounts); err != nil {
		return fmt.Errorf("%s's accounts: %w", x.Value("host"), err)
	}
	want, err := sliceTree(x, x.Value("main_rev"), x.Value("host"), l, accounts)
	if err != nil {
		return err
	}
	if got, _ := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "rev-parse", x.Value("branch_rev")+"^{tree}"); got != want {
		return fmt.Errorf("%s was not published from %s: run fleet inventory publish %s, then this again",
			x.Value("config_ref"), x.Value("main_rev"), x.Value("host"))
	}
	x.UI.OK(x.Value("config_ref") + " is published from " + x.Value("main_rev"))
	x.Values["inv"] = x.Value("branch_rev")
	return nil
}

// checkSource makes sure the installer can fetch the commit to install from
// where the line says: fleet-manager itself for its own checkout, or else
// the repository the host syncs from, which the checkout must be.
func checkSource(x *engine.Ctx) error {
	origin, url := x.Value("origin"), x.Value("config_url")
	switch {
	case x.Options.DryRun:
	case x.Value("fm") == x.Value("inv"):
		if !sameRepo(origin, fleet.UpstreamURL) {
			return fmt.Errorf("this checkout fetches %s, not %s, from where the installer fetches fleet-manager's commits", origin, fleet.UpstreamURL)
		}
	case url == "":
		return fmt.Errorf("%s names no repository to sync from (conf.fleet.repo.url), so the installer would not know where to fetch it from", x.Value("host"))
	case !sameRepo(origin, url):
		return fmt.Errorf("this checkout fetches %s, but %s installs from %s: run this in a checkout of that", origin, x.Value("host"), url)
	}
	return nil
}

// fullHash is a commit as git writes it in full.
var fullHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// sameRepo compares two git URLs of a GitHub repository however they are
// written; others must match as they are.
func sameRepo(a, b string) bool {
	ma, mb := githubRemote.FindStringSubmatch(a), githubRemote.FindStringSubmatch(b)
	if ma != nil && mb != nil {
		return strings.EqualFold(ma[1], mb[1])
	}
	return a == b
}

func checkInstallerCommit(x *engine.Ctx) error {
	inv := x.Value("inv")
	fm := inv
	lock, err := os.ReadFile(filepath.Join(x.Value("flake"), "flake.lock"))
	if err != nil {
		return err
	}
	var l struct {
		Nodes map[string]struct {
			Locked struct {
				Rev string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(lock, &l); err != nil {
		return fmt.Errorf("flake.lock: %w", err)
	}
	// A configuration of its own pins fleet-manager; fleet-manager's own
	// checkout is both.
	if n, ok := l.Nodes["fleet-manager"]; ok {
		fm = n.Locked.Rev
		if !fullHash.MatchString(fm) {
			return errors.New("flake.lock pins fleet-manager by no commit: lock it at one whose signature you checked")
		}
	}
	x.Values["fm"] = fm
	if err := checkSource(x); err != nil {
		return err
	}
	if url := x.Value("config_url"); url != "" {
		x.UI.OK(x.Value("host") + " syncs from " + url)
	}
	if fm == inv {
		x.UI.OK("the same commit: this is fleet-manager's own checkout")
		x.Values["fm_git"] = x.Value("flake")
	} else {
		// The pin, and every commit it brings over the one you trusted last on
		// fleet-manager's main, against your copy of fleet-manager's keys.
		defer releaseFleetManagerHistory(x)
		if err := verifyFleetManagerPin(x, fm, ""); err != nil {
			return err
		}
	}
	res, err := engine.Cmd("git", "-C", "{{.fm_git}}", "show", "{{.fm}}:bootstrap").ReadOnly().Quiet().OK("read bootstrap at {{.fm}}").Run(x)
	if err != nil {
		return err
	}
	// An older bootstrap ignores REV and FLAKE's ?rev=, and would install what
	// main has: the line would look pinned and not be.
	if !x.Options.DryRun && !strings.Contains(res.Stdout, "\n# pinned-installs: 1 ") {
		return fmt.Errorf("fleet-manager's %s predates pinned installs: its bootstrap would install what main has; "+
			"lock the configuration at a newer fleet-manager", fm)
	}
	sum := sha256.Sum256([]byte(res.Stdout))
	x.Values["bootstrap_sum"] = hex.EncodeToString(sum[:])
	x.UI.OK("bootstrap at that commit has SHA-256 " + x.Value("bootstrap_sum"))
	return nil
}

// printInstallLine prints the line for the installer. A configuration with a
// repository of its own is named, at its commit, as FLAKE; fleet-manager's
// own hosts are bootstrap's default at REV.
func printInstallLine(x *engine.Ctx) error {
	fm, inv := x.Value("fm"), x.Value("inv")
	env := "REV=" + fm
	if fm != inv {
		env += " FLAKE=" + engine.Quote(flakeAt(x.Value("config_url"), x.Value("config_ref"), inv))
	}
	x.UI.Text("curl -sSfL " + fleet.UpstreamRaw + "/" + fm + "/bootstrap -o /tmp/bootstrap \\\n" +
		"  && echo '" + x.Value("bootstrap_sum") + "  /tmp/bootstrap' | sha256sum -c \\\n" +
		"  && " + env + " sh /tmp/bootstrap " + x.Value("host"))
	return nil
}

// flakeAt is a configuration repository's git URL as a flake reference to
// commit rev of branch ref: git@host:path and ssh:// become git+ssh://, https
// git+https://. The installer clones that branch alone.
func flakeAt(url, ref, rev string) string {
	query := "?ref=" + ref + "&rev=" + rev
	switch {
	case scpURL.MatchString(url):
		userHost, path, _ := strings.Cut(url, ":")
		url = "ssh://" + userHost + "/" + strings.TrimPrefix(path, "/")
	case !strings.HasPrefix(url, "ssh://") && !strings.HasPrefix(url, "https://"):
		return url + query
	}
	return "git+" + url + query
}
