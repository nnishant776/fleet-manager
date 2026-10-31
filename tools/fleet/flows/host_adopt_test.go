package flows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const (
	adoptHash = "$y$j9T$saltsalt$HASHHASHHASHHASHHASHHASHHASHHASHHASHHASHHAS"
	adoptKey1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx alice@laptop"
	adoptKey2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOtherOtherOtherOtherOtherOtherOtherOtherOth alice@phone"
	adoptDef  = `{ ... }: {
  imports = [
    ./hardware.nix
  ];
  nixpkgs.hostPlatform = "x86_64-linux";
}
`
)

// adoptEnv is an inventory with the host laptop1, a secrets repository, and
// the host's answers: its owner, their account and password hash and keys.
type adoptEnv struct {
	*importEnv
	repo        *userRepo
	personal    bool
	privileged  bool
	marker      string // /var/lib/fleet/owner; "" when it is not there
	hash        string
	keys        []string
	def         string // the host's default.nix
	gecos       string // the owner's full name as getent shows it
	layoutHosts string // the layout's hosts dir
}

func newAdoptEnv(t *testing.T) *adoptEnv {
	t.Helper()
	e := newImportEnv(t)
	// The recovery recipient fleet setup caches, in the state folder the
	// environment made.
	state, err := fleet.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "recovery.pub"), []byte("age1recoveryrecipient\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &adoptEnv{importEnv: e, repo: &userRepo{hosts: []string{"laptop1"}}, personal: true, privileged: true,
		marker: "alice", hash: adoptHash, keys: []string{adoptKey1, adoptKey2}, def: adoptDef,
		gecos: `Alice "Al" Example,,,`, layoutHosts: "config/hosts"}
	return a
}

func (a *adoptEnv) hostDir() string { return filepath.Join(a.inv, "config", "hosts", "laptop1") }

func (a *adoptEnv) write() {
	a.t.Helper()
	if err := os.MkdirAll(a.hostDir(), 0o755); err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.hostDir(), "default.nix"), []byte(a.def), 0o644); err != nil {
		a.t.Fatal(err)
	}
}

func (a *adoptEnv) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	ok := func(out string) (engine.Result, error) { return engine.Result{Stdout: out}, nil }
	fail := func(stderr string) (engine.Result, error) {
		return engine.Result{Stderr: stderr, Code: 1}, errors.New("exited with code 1")
	}
	switch s.Name {
	case "ssh":
		script := s.Args[len(s.Args)-1]
		switch {
		case script == "id -un; hostname":
			return ok("support\nlaptop1\n")
		case strings.Contains(script, "cat /var/lib/fleet/owner"):
			if a.marker == "" {
				return fail("cat: /var/lib/fleet/owner: No such file or directory")
			}
			return ok(a.marker + "\n")
		case strings.HasPrefix(script, "getent passwd "):
			return ok("alice:x:1000:100:" + a.gecos + ":/home/alice:/run/current-system/sw/bin/bash\n")
		case strings.HasPrefix(script, "sudo -n getent shadow "):
			return ok("alice:" + a.hash + ":19000:0:99999:7:::\n")
		case strings.Contains(script, "authorized_keys'") && strings.Contains(script, "sudo -n cat"):
			if len(a.keys) == 0 {
				return ok("")
			}
			return ok("# the owner's\n" + strings.Join(a.keys, "\n") + "\n")
		}
		return ok("")
	case "nix":
		switch {
		case strings.HasSuffix(args, "conf.fleet.personal"):
			p := "false"
			if a.personal {
				p = "true"
			}
			v := "false"
			if a.privileged {
				v = "true"
			}
			return ok(`{"enable":` + p + `,"ownerPrivileged":` + v + `}`)
		case strings.Contains(args, "#fleetLayout"):
			return ok(`{"hostBranches":true,"hosts":"` + a.layoutHosts + `","users":"config/users","shared":[]}`)
		}
		return a.importEnv.respond(s)
	case "git":
		if strings.Contains(args, "-C "+a.inv+" ") {
			return a.importEnv.respond(s)
		}
	case "sh":
		return a.importEnv.respond(s)
	}
	return a.repo.respond(s)
}

func (a *adoptEnv) run(flags map[string]string, mutate func(*engine.Ctx)) (error, *engine.Fake, string) {
	a.t.Helper()
	a.write()
	x, fake, out := newFakeCtx(a.respond)
	if mutate != nil {
		mutate(x)
	}
	flags["flake"], flags["host"], flags["repo"], flags["recovery"] = a.inv, "laptop1", a.t.TempDir(), ""
	err := engine.RunWith(x, HostAdopt, flags)
	return err, fake, out.String()
}

// nothingChanged fails for anything the run wrote: in the inventory, the
// secrets or git.
func (a *adoptEnv) nothingChanged(t *testing.T, fake *engine.Fake, out string) {
	t.Helper()
	if fileExists(filepath.Join(a.hostDir(), "owner.nix")) {
		t.Errorf("owner.nix is there")
	}
	if got, _ := os.ReadFile(filepath.Join(a.hostDir(), "default.nix")); string(got) != a.def {
		t.Errorf("default.nix changed:\n%s", got)
	}
	for _, c := range fake.Commands() {
		for _, w := range []string{" add -- ", " commit ", " push ", "sops set", "sops --encrypt", "sops rotate", "worktree add", "mkpasswd"} {
			if strings.Contains(c, w) {
				t.Errorf("changed something: %s", c)
			}
		}
	}
}

// inventoryUntouched fails when the run wrote into the inventory: a file, or
// any git command that stages, commits or pushes there.
func (a *adoptEnv) inventoryUntouched(t *testing.T, fake *engine.Fake) {
	t.Helper()
	if fileExists(filepath.Join(a.hostDir(), "owner.nix")) {
		t.Errorf("owner.nix is there")
	}
	if got, _ := os.ReadFile(filepath.Join(a.hostDir(), "default.nix")); string(got) != a.def {
		t.Errorf("default.nix changed:\n%s", got)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "-C "+a.inv+" ") || strings.Contains(c, "HEAD:refs/heads/main") {
			for _, w := range []string{" add ", " commit ", " push ", " reset ", " checkout ", " commit-tree "} {
				if strings.Contains(c, w) {
					t.Errorf("wrote into the inventory: %s", c)
				}
			}
		}
	}
}

func TestHostAdoptRefusesAHostThatIsNotPersonal(t *testing.T) {
	a := newAdoptEnv(t)
	a.personal = false
	err, fake, out := a.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "laptop1 is not a personal machine: there is nothing to adopt") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if ran(fake, "ssh") {
		t.Errorf("reached the host")
	}
	a.nothingChanged(t, fake, out)
}

func TestHostAdoptRefusesAHostNobodyHasSetUp(t *testing.T) {
	a := newAdoptEnv(t)
	a.marker = ""
	err, fake, out := a.run(map[string]string{}, nil)
	if err == nil || !strings.Contains(out, "nobody has set laptop1 up yet") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	a.nothingChanged(t, fake, out)
}

func TestHostAdoptRefusesAnOwnerWithoutAPassword(t *testing.T) {
	for _, hash := range []string{"!", "!!", "*", ""} {
		a := newAdoptEnv(t)
		a.hash = hash
		err, fake, out := a.run(map[string]string{}, nil)
		if err == nil || !strings.Contains(out, "alice has no password; have them set one with passwd first") {
			t.Fatalf("%q: went ahead, or without saying why: %v\n%s", hash, err, out)
		}
		a.nothingChanged(t, fake, out)
	}
}

func TestHostAdoptStoresTheOwnersSecretsAndPrintsTheDeclaration(t *testing.T) {
	a := newAdoptEnv(t)
	err, fake, out := a.run(map[string]string{}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The hash goes to sops on stdin, as the JSON string; it is in no argument
	// and nowhere on the screen.
	var stored, keysStored bool
	for _, s := range fake.Ran {
		for _, arg := range s.Args {
			if strings.Contains(arg, adoptHash) {
				t.Errorf("the hash is in an argument of %s", s)
			}
		}
		if strings.Contains(s.Name, adoptHash) {
			t.Errorf("the hash is in the name of %s", s)
		}
		if s.Name == "sops" && s.Args[0] == "set" && s.Stdin == `"`+adoptHash+`"` {
			stored = true
		}
		if s.Name == "sops" && s.Args[0] == "set" && strings.Contains(s.Stdin, adoptKey1) && strings.Contains(s.Stdin, adoptKey2) {
			keysStored = true
		}
		if s.Name == "mkpasswd" {
			t.Errorf("hashed a password: %s", s)
		}
	}
	if !stored {
		t.Errorf("sops set did not get the hash as it is on stdin:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	if !keysStored {
		t.Errorf("sops set did not get the owner's keys:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	if strings.Contains(out, adoptHash) || strings.Contains(out, "HASHHASH") {
		t.Errorf("showed the hash:\n%s", out)
	}
	inOrder(t, fake,
		`sops set --value-stdin users/alice/laptop1.yaml '["password"]'`,
		`sops set --value-stdin users/alice/laptop1.yaml '["authorized_keys"]'`,
		"git commit -q -m 'user-keys: alice on laptop1'",
		"push origin host/laptop1",
	)
	a.inventoryUntouched(t, fake)

	next := out[strings.LastIndex(out, "alice's password hash"):]
	for _, want := range []string{
		"alice's password hash and keys are in the secrets. Now declare the account in the inventory and take the host out of personal mode, in config/hosts/laptop1/default.nix (the layout's hosts dir):",
		"conf.users.accounts.alice = {",
		`fullName = "Alice \"Al\" Example";`,
		"privileged = true;",
		"secrets.password = true;",
		"secrets.authorizedKeys = true;",
		"and remove conf.fleet.personal.enable = true (or set it to false).",
		"fleet inventory publish laptop1 --flake " + a.inv + " --sync",
		"fleet host check laptop1 --flake " + a.inv,
		"alice keeps their uid, files and password.",
		"their password and keys change through you, fleet secrets user-password / user-keys",
		"passwd on the machine no longer sticks, and ~/.ssh/authorized_keys no longer admits them.",
	} {
		if !strings.Contains(next, want) {
			t.Errorf("the next steps lack %q:\n%s", want, next)
		}
	}
	// Nothing is declared with an id, and the text never names one.
	for _, no := range []string{"uid =", "gid =", "owner.nix", "uid 1000"} {
		if strings.Contains(next, no) {
			t.Errorf("the next steps hold %q:\n%s", no, next)
		}
	}
	t.Logf("the next steps:\n%s", next)
}

func TestHostAdoptDeclaresAnOrdinaryOwnerWithoutKeys(t *testing.T) {
	a := newAdoptEnv(t)
	a.privileged = false
	a.keys = nil
	err, fake, out := a.run(map[string]string{}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	next := out[strings.LastIndex(out, "alice's password hash"):]
	for _, want := range []string{"alice's password hash is in the secrets.", "privileged = false;", "secrets.password = true;"} {
		if !strings.Contains(next, want) {
			t.Errorf("the next steps lack %q:\n%s", want, next)
		}
	}
	if strings.Contains(next, "authorizedKeys") {
		t.Errorf("declares keys that were not stored:\n%s", next)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "authorized_keys'") && strings.Contains(c, "sops set") {
			t.Errorf("stored keys that there are none of: %s", c)
		}
	}
	if !strings.Contains(flat(out), "the owner has no login keys") {
		t.Errorf("did not say the key step was left out:\n%s", out)
	}
}

func TestHostAdoptEscapesTheFullNameAsANixString(t *testing.T) {
	a := newAdoptEnv(t)
	a.gecos = `Al "A" \ ${x}`
	err, _, out := a.run(map[string]string{}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := `fullName = "Al \"A\" \\ \${x}";`; !strings.Contains(out, want) {
		t.Errorf("the next steps lack %q:\n%s", want, out)
	}
}

func TestHostAdoptTakesTheHostsFolderFromTheLayout(t *testing.T) {
	a := newAdoptEnv(t)
	a.layoutHosts = "machines"
	err, _, out := a.run(map[string]string{}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "in machines/laptop1/default.nix (the layout's hosts dir)") {
		t.Errorf("did not use the layout's folder:\n%s", out)
	}
}

func TestHostAdoptEnrollsTheOwnerFirstWithTheirEnrollmentFile(t *testing.T) {
	a := newAdoptEnv(t)
	from := filepath.Join(t.TempDir(), "alice.enroll")
	if err := os.WriteFile(from, []byte(userEnrollDoc("alice", aliceAge, aliceKey)), 0o644); err != nil {
		t.Fatal(err)
	}
	err, fake, out := a.run(map[string]string{"from": from}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"git commit -q -m 'user-enroll: alice'",
		"git push origin user/alice",
		`sops set --value-stdin users/alice/laptop1.yaml '["password"]'`,
		"git commit -q -m 'user-password: alice@laptop1'",
		`sops set --value-stdin users/alice/laptop1.yaml '["authorized_keys"]'`,
	)
	for _, s := range fake.Ran {
		for _, arg := range s.Args {
			if strings.Contains(arg, adoptHash) {
				t.Errorf("the hash is in an argument of %s", s)
			}
		}
	}
	a.inventoryUntouched(t, fake)
	for _, want := range []string{"alice's password hash, keys and enrollment are in the secrets.", "they change their password and keys with fleet user password / fleet user keys"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("the next steps lack %q:\n%s", want, out)
		}
	}
	// The name the commands use for the owner is the owner's, and the host's
	// is the host's, again after them.
	if !strings.Contains(strings.Join(fake.Commands(), "\n"), "support@laptop1") {
		t.Errorf("lost the account the host is reached as")
	}
}

func TestHostAdoptDryRunChangesNothing(t *testing.T) {
	a := newAdoptEnv(t)
	err, fake, out := a.run(map[string]string{"from": "/no/such/file"}, func(x *engine.Ctx) { x.Options.DryRun = true })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	a.nothingChanged(t, fake, out)
	if ran(fake, "age -d") {
		t.Errorf("asked to unlock the identity")
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "systemctl start") || strings.Contains(c, " commit-tree ") {
			t.Errorf("changed something: %s", c)
		}
	}
	for _, want := range []string{"dry run", "fleet secrets user-enroll alice", "fleet secrets user-password alice laptop1 --hash-stdin", "fleet secrets user-keys alice laptop1"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, adoptHash) {
		t.Errorf("showed the hash:\n%s", out)
	}
}

func TestNixString(t *testing.T) {
	for in, want := range map[string]string{
		`Ann Example`:  `"Ann Example"`,
		`Ann "A" \ B`:  `"Ann \"A\" \\ B"`,
		"line\nbreak":  `"line\nbreak"`,
		"cost ${x} $5": `"cost \${x} $5"`,
	} {
		if got := nixString(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}
