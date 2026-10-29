package flows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const (
	impHead  = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1" // the inventory's main
	impNew   = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2" // after the commit
	impFM    = "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3" // fleet-manager's checkout
	impBranc = "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4" // the host's branch commit
	impKey   = "me namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx\n"
)

const impMini = `{ ... }: {
  imports = [ ./hardware.nix ];
  nixpkgs.hostPlatform = "x86_64-linux";
  conf.fleet.repo.url = "https://github.com/nnishant776/fleet-manager";
  conf.fleet.signing.allowedSignersFile = ../../fleet/allowed_signers;
}
`

// importEnv is an inventory checkout, and a checkout of fleet-manager whose
// commit holds the files in fm, in folders of the test's own.
type importEnv struct {
	t         *testing.T
	inv, fm   string
	fmFiles   map[string]string // path in the commit to text
	accounts  string            // the host's accounts, as JSON
	users     map[string]bool   // the users folders the commit has
	ref       string            // what the host's conf.fleet.repo.ref evaluates to
	repoURL   string            // the host's repo.url, "" to read it from the copied file
	secrets   string            // the source host's conf.secrets.repo.url, as JSON
	committed bool
}

func newImportEnv(t *testing.T) *importEnv {
	t.Helper()
	trustCopies(t, invAddress, fleet.UpstreamURL)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	e := &importEnv{t: t, inv: t.TempDir(), fm: t.TempDir(), accounts: "[]", users: map[string]bool{}, ref: "host/mini", secrets: `"ssh://git@forge/secrets.git"`,
		fmFiles: map[string]string{
			"hosts/mini/default.nix":  impMini,
			"hosts/mini/hardware.nix": "{ ... }: { fileSystems.\"/\".device = \"/dev/disk/by-label/nixos\"; }\n",
			"hosts/other/default.nix": "{ ... }: { } # mini is the other one\n",
		}}
	if err := os.MkdirAll(filepath.Join(e.inv, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.inv, "fleet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.inv, "fleet", "allowed_signers"), []byte(impKey), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *importEnv) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	ok := func(out string) (engine.Result, error) { return engine.Result{Stdout: out}, nil }
	switch s.Name {
	case "sh":
		if strings.Contains(args, `archive "$2" -- "$3"`) {
			// The files of the commit under the path, into the folder.
			rel, dest := s.Args[len(s.Args)-2], s.Args[len(s.Args)-1]
			for p, text := range e.fmFiles {
				if p == rel || strings.HasPrefix(p, rel+"/") {
					full := filepath.Join(dest, filepath.FromSlash(p))
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						e.t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
						e.t.Fatal(err)
					}
				}
			}
		}
		return ok("")
	case "nix":
		switch {
		case strings.Contains(args, "#fleetLayout"):
			return ok(`{"hostBranches":true,"hosts":"config/hosts","users":"config/users","shared":[]}`)
		case strings.Contains(args, "git+file://") && strings.Contains(args, "conf.secrets.repo.url"):
			return ok(e.secrets)
		case strings.Contains(args, "git+file://"):
			return ok(e.accounts)
		case strings.Contains(args, "c.config.conf.fleet.repo.url) cs"):
			return ok(`{"alpha":"ssh://git@forge/inventory.git"}`)
		case strings.Contains(args, "attrNames c.config.conf.users.accounts"):
			if strings.Contains(args, "darwinConfigurations") {
				return ok(`{}`)
			}
			return ok(`{"alpha":[],"mini":[]}`)
		case strings.Contains(args, "darwinConfigurations"):
			return ok(`{}`)
		case strings.Contains(args, "toplevel.drvPath) cs"):
			return ok(`{"alpha":"/nix/store/a.drv","mini":"/nix/store/m.drv"}`)
		case strings.Contains(args, "conf.fleet.repo.url"):
			if e.repoURL != "" {
				return ok(`"` + e.repoURL + `"`)
			}
			data, err := os.ReadFile(filepath.Join(e.inv, "config", "hosts", "mini", "default.nix"))
			if err != nil {
				return engine.Result{Stderr: "not copied"}, err
			}
			for _, l := range strings.Split(string(data), "\n") {
				if strings.Contains(l, "conf.fleet.repo.url") {
					return ok(l[strings.Index(l, `"`) : strings.LastIndex(l, `"`)+1])
				}
			}
			return ok("null")
		case strings.Contains(args, "conf.fleet.repo.ref"):
			return ok(`"` + e.ref + `"`)
		case strings.Contains(args, "allowedSignersFile"):
			return ok(impKey)
		case strings.Contains(args, "conf.secrets.repo.url"):
			return ok(`""`)
		case strings.Contains(args, "path:"):
			return ok("/nix/store/mini-branch.drv")
		}
		e.t.Fatalf("unexpected nix: %s", s)
	case "git":
		switch {
		case strings.Contains(args, "-C "+e.fm+" remote get-url origin"):
			return ok(fleet.UpstreamURL + "\n")
		case strings.Contains(args, "-C "+e.fm+" rev-parse --verify -q HEAD^{commit}"):
			return ok(impFM + "\n")
		case strings.Contains(args, "-C "+e.fm+" rev-parse --verify -q refs/remotes/origin/main^{commit}"):
			return ok(impFM + "\n")
		case strings.Contains(args, "-C "+e.fm+" ls-tree"):
			switch {
			case strings.HasSuffix(args, "-- hosts/mini"):
				return ok("hosts/mini\n")
			case strings.HasSuffix(args, "-- hosts/"):
				return ok("hosts/mini\nhosts/other\n")
			}
			for u := range e.users {
				if strings.HasSuffix(args, "-- users/"+u) {
					return ok("users/" + u + "\n")
				}
			}
			return ok("")
		case strings.Contains(args, "-C "+e.fm+" grep"):
			return ok(impFM + ":hosts/other/default.nix\n")
		case strings.Contains(args, "-C "+e.inv+" rev-parse --verify -q HEAD^{commit}"):
			if e.committed {
				return ok(impNew + "\n")
			}
			return ok(impHead + "\n")
		case strings.Contains(args, "-C "+e.inv+" rev-parse --verify -q refs/remotes/origin/main^{commit}"):
			return ok(impHead + "\n")
		case strings.Contains(args, "-C "+e.inv+" rev-parse HEAD"):
			return ok(impNew + "\n")
		case strings.Contains(args, "-C "+e.inv+" branch -r --contains"):
			return ok("origin/main\n")
		case strings.Contains(args, "-C "+e.inv+" show "+impNew+":flake.lock"):
			return ok("")
		case strings.HasSuffix(args, "write-tree"):
			return ok("tree-mini\n")
		case strings.Contains(args, " commit-tree "):
			return ok(impBranc + "\n")
		case strings.Contains(args, "-C "+e.inv+" commit "):
			e.committed = true
		}
	}
	return engine.Result{}, nil
}

func (e *importEnv) run(flags map[string]string, mutate func(*engine.Ctx)) (error, *engine.Fake, string) {
	x, fake, out := newFakeCtx(e.respond)
	if mutate != nil {
		mutate(x)
	}
	flags["flake"], flags["host"], flags["from"] = e.inv, "mini", e.fm
	err := engine.RunWith(x, InventoryImport, flags)
	return err, fake, out.String()
}

func wrote(fake *engine.Fake) []string {
	var out []string
	for _, c := range fake.Commands() {
		if strings.Contains(c, " add -- ") || strings.Contains(c, " commit ") || strings.Contains(c, " push ") {
			out = append(out, c)
		}
	}
	return out
}

func TestInventoryImportRefusesAHostTheInventoryHasAlready(t *testing.T) {
	e := newImportEnv(t)
	if err := os.MkdirAll(filepath.Join(e.inv, "config", "hosts", "mini"), 0o755); err != nil {
		t.Fatal(err)
	}
	err, fake, out := e.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "mini is in the inventory already") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if w := wrote(fake); len(w) > 0 {
		t.Errorf("changed something: %v", w)
	}
	if ran(fake, "sh -c") {
		t.Errorf("read the host anyway")
	}
}

func TestInventoryImportRefusesAFileThatReadsOutsideItsFolder(t *testing.T) {
	e := newImportEnv(t)
	e.fmFiles["hosts/mini/default.nix"] = "{ ... }: { imports = [ ../../shared/x.nix ]; }\n"
	err, fake, out := e.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "hosts/mini/default.nix reads ../../shared/x.nix, outside its folder: copy it into the folder, or into the inventory and list it in shared in its flake.nix, then run this again") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if fileExists(filepath.Join(e.inv, "config")) {
		t.Errorf("wrote into the inventory")
	}
	if w := wrote(fake); len(w) > 0 {
		t.Errorf("changed something: %v", w)
	}
}

func TestInventoryImportTakesTheCopyOutWhenTheHostSetsItsRef(t *testing.T) {
	e := newImportEnv(t)
	e.ref = "main"
	err, fake, out := e.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "mini sets conf.fleet.repo.ref itself") || !strings.Contains(out, "the inventory sets host/mini") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if fileExists(filepath.Join(e.inv, "config", "hosts", "mini")) {
		t.Errorf("the copy is still in the inventory")
	}
	if !ran(fake, "git -C "+e.inv+" reset -q -- config/hosts/mini") {
		t.Errorf("did not unstage:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	for _, c := range wrote(fake) {
		if strings.Contains(c, " commit ") || strings.Contains(c, " push ") {
			t.Errorf("went on: %s", c)
		}
	}
}

func TestInventoryImportMovesTheHost(t *testing.T) {
	e := newImportEnv(t)
	e.accounts = `["alice","bob"]`
	e.users["alice"] = true
	e.fmFiles["users/alice/default.nix"] = "{ ... }: { k = ../../fleet/allowed_signers; }\n"
	err, fake, out := e.run(map[string]string{}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, _ := os.ReadFile(filepath.Join(e.inv, "config", "hosts", "mini", "default.nix"))
	for _, want := range []string{"../../../fleet/allowed_signers", `"ssh://git@forge/inventory.git"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the copy lacks %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "github.com/nnishant776") {
		t.Errorf("the copy names fleet-manager:\n%s", data)
	}
	if hw, _ := os.ReadFile(filepath.Join(e.inv, "config", "hosts", "mini", "hardware.nix")); string(hw) != e.fmFiles["hosts/mini/hardware.nix"] {
		t.Errorf("hardware.nix changed: %q", hw)
	}
	if u, _ := os.ReadFile(filepath.Join(e.inv, "config", "users", "alice", "default.nix")); !strings.Contains(string(u), "../../../fleet/allowed_signers") {
		t.Errorf("the user's file is %q", u)
	}
	if fileExists(filepath.Join(e.inv, "config", "users", "bob")) {
		t.Errorf("copied a user with no folder")
	}
	var commit, push string
	var branchPush bool
	for _, s := range fake.Ran {
		c := s.String()
		switch {
		case strings.Contains(c, "-C "+e.inv+" commit "):
			commit = strings.Join(s.Args, "\n")
		case strings.HasSuffix(c, "push origin HEAD:refs/heads/main"):
			push = c
		case strings.Contains(c, "push --atomic origin "+impBranc+":refs/heads/host/mini"):
			branchPush = true
		}
	}
	for _, want := range []string{"feat(hosts): mini moves here from fleet-manager", "Copied from fleet-manager at " + fleet.Short(impFM),
		"it now fetches ssh://git@forge/inventory.git", "config/hosts/mini", "config/users/alice"} {
		if !strings.Contains(commit, want) {
			t.Errorf("the commit lacks %q:\n%s", want, commit)
		}
	}
	if push == "" {
		t.Errorf("did not push main")
	}
	if !branchPush {
		t.Errorf("did not publish host/mini:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	if !ran(fake, "git -C "+e.inv+" add -- config/hosts/mini config/users/alice") {
		t.Errorf("did not add the files before evaluating")
	}
	if r := trustedOf(t, invAddress, "main"); r != impNew {
		t.Errorf("the trusted main is %q, want the pushed %s", r, impNew)
	}
	if r := trustedOf(t, invAddress, "host/mini"); r != impBranc {
		t.Errorf("the trusted host/mini is %q, want %s", r, impBranc)
	}
	next := flat(out)
	for _, want := range []string{"fleet host enroll mini --flake " + e.inv + " --repo <your secrets checkout>",
		"fleet host check mini --flake " + e.inv, "git rm -r hosts/mini users/alice", "hosts/other/default.nix"} {
		if !strings.Contains(next, want) {
			t.Errorf("the next steps lack %q:\n%s", want, out)
		}
	}
	if ran(fake, "git -C "+e.inv+" reset") {
		t.Errorf("took the copy out after a completed run")
	}
}

func TestInventoryImportNamesTheFlakeAsGiven(t *testing.T) {
	e := newImportEnv(t)
	err, _, out := e.run(map[string]string{}, func(x *engine.Ctx) { x.Values["flake_name"] = "inventory" })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "fleet host check mini --flake inventory") {
		t.Errorf("did not name the inventory:\n%s", out)
	}
}

func TestInventoryImportStopsOnAUserTheInventoryHasWithOtherFiles(t *testing.T) {
	e := newImportEnv(t)
	e.accounts = `["alice"]`
	e.users["alice"] = true
	e.fmFiles["users/alice/default.nix"] = "{ ... }: { }\n"
	dir := filepath.Join(e.inv, "config", "users", "alice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.nix"), []byte("{ ... }: { other = 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err, fake, out := e.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "config/users/alice is in the inventory already, with other files") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if w := wrote(fake); len(w) > 0 {
		t.Errorf("changed something: %v", w)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "default.nix")); string(got) != "{ ... }: { other = 1; }\n" {
		t.Errorf("changed the inventory's user: %q", got)
	}
}

func TestInventoryImportDryRunWritesNothing(t *testing.T) {
	e := newImportEnv(t)
	err, fake, out := e.run(map[string]string{}, func(x *engine.Ctx) { x.Options.DryRun = true })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fileExists(filepath.Join(e.inv, "config")) {
		t.Errorf("wrote into the inventory")
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, " add -- ") || strings.Contains(c, " commit ") || strings.Contains(c, " push ") || strings.Contains(c, " commit-tree ") {
			t.Errorf("changed something: %s", c)
		}
	}
	if r := trustedOf(t, invAddress, "host/mini"); r != "" {
		t.Errorf("a dry run recorded %q", r)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("did not say it was a dry run:\n%s", out)
	}
}

// A host without secrets has no identity, so no configuration key to fetch
// the inventory with, and no secrets to take its login keys from: it is
// refused before anything is copied, saying what to do in fleet-manager first.
func TestInventoryImportRefusesAHostWithoutSecrets(t *testing.T) {
	for _, secrets := range []string{"null", `""`} {
		e := newImportEnv(t)
		e.secrets = secrets
		err, fake, out := e.run(map[string]string{}, nil)
		if err == nil || !strings.Contains(out, "mini has no secrets: conf.secrets.repo.url is not set") ||
			!strings.Contains(out, "fleet host enroll mini") {
			t.Fatalf("%s: went ahead, or without saying why: %v\n%s", secrets, err, out)
		}
		if fileExists(filepath.Join(e.inv, "config")) {
			t.Errorf("%s: wrote into the inventory", secrets)
		}
		if w := wrote(fake); len(w) > 0 {
			t.Errorf("%s: changed something: %v", secrets, w)
		}
	}
}
