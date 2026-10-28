package flows

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const (
	pinOld  = "1111111111111111111111111111111111111111"
	pinNew  = "2222222222222222222222222222222222222222"
	pinHead = "3333333333333333333333333333333333333333"
	pinDone = "4444444444444444444444444444444444444444"
	// Where two commits that follow no common line part, for merge-base.
	pinBase = "8888888888888888888888888888888888888888"
)

// pinEnv is an inventory checkout with a flake.lock, and the tools' clone of
// fleet-manager, in folders of the test's own.
type pinEnv struct {
	t        *testing.T
	flake    string
	fm       string
	dirty    bool
	headSame bool // HEAD is origin's main
	// ancestors lists "a b" pairs where a is an ancestor of b.
	ancestors map[string]bool
	untrusted map[string]bool // commits of fleet-manager no trusted key signed
	log       map[string]string
	sigs      map[string]string // "base..tip" to the signature listing git log gives
	lockFails bool              // the hosts do not evaluate once the lock changed
	locked    bool
	committed bool
	pushedAt  string
}

func newPinEnv(t *testing.T, lockURL string) *pinEnv {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	e := &pinEnv{t: t, headSame: true, ancestors: map[string]bool{pinOld + " " + pinNew: true}, untrusted: map[string]bool{}, log: map[string]string{}, sigs: map[string]string{}}
	e.flake = t.TempDir()
	if err := os.MkdirAll(filepath.Join(e.flake, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if lockURL != "" {
		lock := `{"nodes":{"fleet-manager":{"locked":{"type":"git","url":"` + lockURL + `","rev":"` + pinOld + `"}},"root":{"inputs":{"fleet-manager":"fleet-manager"}}},"root":"root","version":7}`
		if err := os.WriteFile(filepath.Join(e.flake, "flake.lock"), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The keys you trust for the inventory, and for fleet-manager.
	for _, address := range []string{invAddress, fleet.UpstreamURL} {
		p, err := fleet.SignersCopy(address)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("me namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// withFleetManager names fleet-manager's clone and makes its folder.
func (e *pinEnv) withFleetManager() {
	id, _ := fleet.RepoID(fleet.UpstreamURL)
	if err := fleet.SetName("fleet-manager", id); err != nil {
		e.t.Fatal(err)
	}
	dir, err := fleet.ClonePathOfID(id)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	e.fm = dir
}

func (e *pinEnv) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	fail := engine.Result{Code: 1}
	switch {
	case s.Name == "nix" && len(s.Args) > 1 && s.Args[0] == "flake":
		e.locked = true
	case s.Name == "nix" && strings.Contains(args, "darwinConfigurations"):
		return engine.Result{Stderr: "error: flake does not provide attribute 'darwinConfigurations'"}, errors.New("exited with code 1")
	case s.Name == "nix" && strings.Contains(args, "nixosConfigurations"):
		if e.locked && e.lockFails {
			return engine.Result{Stderr: "error: host nas does not evaluate"}, errors.New("exited with code 1")
		}
		if e.locked {
			return engine.Result{Stdout: `{"nas":"/nix/store/nas-2.drv","pi":"/nix/store/pi-1.drv"}`}, nil
		}
		return engine.Result{Stdout: `{"nas":"/nix/store/nas-1.drv","pi":"/nix/store/pi-1.drv"}`}, nil
	case s.Name != "git":
		return engine.Result{}, nil
	case strings.HasPrefix(args, "-C "+e.flake+" status"):
		if e.dirty {
			return engine.Result{Stdout: " M flake.nix\n"}, nil
		}
	case strings.HasSuffix(args, "-C "+e.flake+" rev-parse --verify -q HEAD^{commit}"):
		if e.committed {
			return engine.Result{Stdout: pinDone + "\n"}, nil
		}
		return engine.Result{Stdout: pinHead + "\n"}, nil
	case strings.HasSuffix(args, "-C "+e.flake+" rev-parse --verify -q refs/remotes/origin/main^{commit}"):
		if e.headSame {
			return engine.Result{Stdout: pinHead + "\n"}, nil
		}
		return engine.Result{Stdout: pinNew + "\n"}, nil
	case strings.HasPrefix(args, "-C "+e.fm+" remote get-url origin"):
		return engine.Result{Stdout: fleet.UpstreamURL + "\n"}, nil
	case strings.HasPrefix(args, "-C "+e.fm+" rev-parse --verify -q refs/remotes/origin/main^{commit}"):
		return engine.Result{Stdout: pinNew + "\n"}, nil
	case strings.Contains(args, "merge-base --is-ancestor"):
		f := strings.Fields(args)
		if e.ancestors[f[len(f)-2]+" "+f[len(f)-1]] {
			return engine.Result{}, nil
		}
		return fail, errors.New("exited with code 1")
	case strings.Contains(args, " merge-base "):
		return engine.Result{Stdout: pinBase + "\n"}, nil
	case strings.Contains(args, " verify-commit "):
		if e.untrusted[s.Args[len(s.Args)-1]] {
			return fail, errors.New("exited with code 1")
		}
	case strings.Contains(args, " log ") && strings.Contains(args, "%G?"):
		return engine.Result{Stdout: e.sigs[s.Args[len(s.Args)-1]]}, nil
	case strings.Contains(args, " log "):
		return engine.Result{Stdout: e.log[s.Args[len(s.Args)-1]]}, nil
	case strings.Contains(args, " commit "):
		e.committed = true
	case strings.Contains(args, " push "):
		e.pushedAt = pinDone
	}
	return engine.Result{}, nil
}

func (e *pinEnv) run(flags map[string]string, yes bool) (error, *engine.Fake, string) {
	x, fake, out := newFakeCtx(e.respond)
	x.Options.Yes = yes
	flags["flake"] = e.flake
	err := engine.RunWith(x, InventoryPin, flags)
	return err, fake, out.String()
}

func ran(fake *engine.Fake, prefix string) bool {
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func changedNothing(t *testing.T, fake *engine.Fake) {
	t.Helper()
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "nix flake") || strings.Contains(c, " commit ") || strings.Contains(c, " push ") {
			t.Errorf("changed something: %s", c)
		}
	}
}

func TestInventoryPinAlreadyPinned(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.untrusted = nil
	e.ancestors = map[string]bool{}
	// origin's main of fleet-manager is the pinned commit.
	orig := e.respond
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if strings.HasPrefix(strings.Join(s.Args, " "), "-C "+e.fm+" rev-parse --verify -q refs/remotes/origin/main^{commit}") {
			return engine.Result{Stdout: pinOld + "\n"}, nil
		}
		return orig(s)
	})
	if err := engine.RunWith(x, InventoryPin, map[string]string{"flake": e.flake}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("did not say so:\n%s", out)
	}
	changedNothing(t, fake)
	if ran(fake, "nix eval") {
		t.Errorf("evaluated hosts")
	}
}

func TestInventoryPinMovesToNewerMain(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.log[pinOld+".."+pinNew] = "bbbbbbb 2026-10-28 fix(nas): a fix\naaaaaaa 2026-10-27 feat(pi): a feature\n"
	var atPush string
	orig := e.respond
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if strings.Contains(strings.Join(s.Args, " "), " push ") {
			atPush = trustedOf(t, invAddress, "main")
		}
		return orig(s)
	})
	if err := engine.RunWith(x, InventoryPin, map[string]string{"flake": e.flake}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	o := out.String()
	for _, want := range []string{"2 commits since the pinned " + fleet.Short(pinOld), "fix(nas): a fix", "nas: its system changes", "pi: stays the same"} {
		if !strings.Contains(o, want) {
			t.Errorf("output lacks %q:\n%s", want, o)
		}
	}
	lock := "nix flake lock --override-input fleet-manager " + engine.Quote("git+https://github.com/nnishant776/fleet-manager?rev="+pinNew)
	var evals int
	var commit, push string
	for _, s := range fake.Ran {
		c := s.String()
		switch {
		case c == lock:
			if s.Dir != e.flake {
				t.Errorf("the lock ran in %q, want %q", s.Dir, e.flake)
			}
		case strings.HasPrefix(c, "nix eval") && strings.Contains(c, "nixosConfigurations"):
			evals++
		case strings.Contains(c, " commit "):
			commit = strings.Join(s.Args, "\n")
		case strings.Contains(c, " push "):
			push = c
		}
	}
	if !ran(fake, lock) {
		t.Errorf("did not lock:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	if evals != 2 {
		t.Errorf("evaluated the hosts %d times, want 2", evals)
	}
	short := fleet.Short(pinNew)
	for _, want := range []string{"build: pin fleet-manager at " + short, "From " + fleet.Short(pinOld) + " to " + short + ": 2 commits.", "Hosts that change: nas.", "flake.lock"} {
		if !strings.Contains(commit, want) {
			t.Errorf("the commit lacks %q:\n%s", want, commit)
		}
	}
	if !strings.HasSuffix(push, "push origin HEAD:refs/heads/main") {
		t.Errorf("push was %q", push)
	}
	if atPush != pinHead {
		t.Errorf("before the push the record was %q, want %s", atPush, pinHead)
	}
	if r := trustedOf(t, invAddress, "main"); r != pinDone {
		t.Errorf("the trusted main is %q, want the pushed %s", r, pinDone)
	}
	if !strings.Contains(o, "fleet inventory publish --flake "+e.flake) {
		t.Errorf("did not say what to run next:\n%s", o)
	}
	if ran(fake, "git -C "+e.flake+" checkout -- flake.lock") {
		t.Errorf("put flake.lock back after a completed run")
	}
}

func TestInventoryPinTheNameIsGivenToPublish(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	x, _, out := newFakeCtx(e.respond)
	x.Values["flake_name"] = "inventory"
	if err := engine.RunWith(x, InventoryPin, map[string]string{"flake": e.flake}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "fleet inventory publish --flake inventory") {
		t.Errorf("did not name the inventory:\n%s", out)
	}
}

// A commit asked for that is not on main is refused before the trusted line is
// looked at (TestInventoryPinRefusesACommitNotOnMain); a main that does not
// follow the commit you trusted is refused when the clone is brought up to date.
func TestInventoryPinRefusesAMainOffTheTrustedLine(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	rec := "5555555555555555555555555555555555555555"
	if err := fleet.WriteTrusted(fleet.UpstreamURL, "main", rec); err != nil {
		t.Fatal(err)
	}
	// The record is not before main's newest commit.
	e.ancestors = map[string]bool{}
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "does not follow "+rec) {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinRefusesAnUnsignedCommit(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	bad := "6666666666666666666666666666666666666666"
	e.untrusted[bad] = true
	err, fake, out := e.run(map[string]string{"to": bad}, true)
	if err == nil || !strings.Contains(out, "not signed by a key you trust") {
		t.Fatalf("went ahead: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinRefusesACheckoutBehindOrigin(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.headSame = false
	e.ancestors[pinHead+" "+pinNew] = true
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "pull first") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinRefusesACheckoutAheadOfOrAwayFromOrigin(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.headSame = false
	e.ancestors[pinNew+" "+pinHead] = true
	if err, _, out := e.run(map[string]string{}, true); err == nil || !strings.Contains(out, "push them, or set them aside") {
		t.Fatalf("ahead: %v\n%s", err, out)
	}
	delete(e.ancestors, pinNew+" "+pinHead)
	if err, _, out := e.run(map[string]string{}, true); err == nil || !strings.Contains(out, "have gone apart") {
		t.Fatalf("apart: %v\n%s", err, out)
	}
}

func TestInventoryPinRefusesUncommittedChanges(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.dirty = true
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "uncommitted changes: commit or discard them first") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinPutsFlakeLockBackWhenAHostDoesNotEvaluate(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.lockFails = true
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "host nas does not evaluate") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if !ran(fake, "git -C "+e.flake+" checkout -- flake.lock") {
		t.Errorf("did not put flake.lock back:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, " commit ") || strings.Contains(c, " push ") {
			t.Errorf("went on: %s", c)
		}
	}
}

func TestInventoryPinAnOlderCommitIsListedAndAsked(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	// The newest main is older than the pin: the pin follows it.
	e.ancestors = map[string]bool{pinNew + " " + pinOld: true}
	e.log[pinNew+".."+pinOld] = "ccccccc 2026-10-20 feat(nas): leaves\n"
	err, fake, out := e.run(map[string]string{}, false)
	if err == nil || !strings.Contains(out, "feat(nas): leaves") || !strings.Contains(out, "older than the pinned") {
		t.Fatalf("did not list the leaving commits, or went ahead: %v\n%s", err, out)
	}
	changedNothing(t, fake)

	// With --yes it is approved.
	if err, fake, out := e.run(map[string]string{}, true); err != nil || !ran(fake, "nix flake lock") {
		t.Fatalf("with --yes: %v\n%s", err, out)
	}
}

func TestInventoryPinAGoneApartPinIsAsked(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.ancestors = map[string]bool{}
	err, fake, out := e.run(map[string]string{}, false)
	if err == nil || !strings.Contains(out, "gone apart") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinNeedsFleetManagersName(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "fleet repo add first") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinRefusesAnotherFleetManager(t *testing.T) {
	e := newPinEnv(t, "https://github.com/example-org/fleet-manager-fork")
	e.withFleetManager()
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "example-org/fleet-manager-fork, not "+fleet.UpstreamURL+": pin that one by hand") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
}

func TestInventoryPinNeedsAFleetManagerInput(t *testing.T) {
	e := newPinEnv(t, "")
	e.withFleetManager()
	if err := os.WriteFile(filepath.Join(e.flake, "flake.lock"), []byte(`{"nodes":{"root":{}},"root":"root","version":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err, _, out := e.run(map[string]string{}, true)
	if err == nil || !strings.Contains(out, "the inventory has no fleet-manager input") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestInventoryPinDryRunChangesNothing(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	x, fake, out := newFakeCtx(e.respond)
	x.Options.DryRun = true
	if err := engine.RunWith(x, InventoryPin, map[string]string{"flake": e.flake}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	changedNothing(t, fake)
	if r := trustedOf(t, invAddress, "main"); r != "" {
		t.Errorf("a dry run recorded %q", r)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("did not say it was a dry run:\n%s", out)
	}
}

// A commit that grew from main on a branch follows the trusted commit, but is
// not on main: hosts would build what was never merged, so it is refused, and
// the record of main stays where it was.
func TestInventoryPinRefusesACommitNotOnMain(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	branch := "7777777777777777777777777777777777777777"
	e.ancestors[pinNew+" "+branch] = true
	err, fake, out := e.run(map[string]string{"to": branch}, true)
	if err == nil || !strings.Contains(err.Error()+out, "is not on fleet-manager's main") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	changedNothing(t, fake)
	if r := trustedOf(t, fleet.UpstreamURL, "main"); r != pinNew {
		t.Errorf("the record of fleet-manager's main is %q, want %s", r, pinNew)
	}
}

// sigLine is one line of what git log prints for the signature check: a hash,
// the status, the key, the subject.
func sigLine(hash, status, key, subject string) string {
	return hash + "\x1f" + status + "\x1f" + key + "\x1f" + subject + "\n"
}

// The signature on the newest commit does not vouch for the ones under it:
// an unsigned commit in the range stops the pin before anything is locked.
func TestInventoryPinRefusesAnUnsignedCommitUnderASignedOne(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	sneaked := "9999999999999999999999999999999999999999"
	e.sigs[pinOld+".."+pinNew] = sigLine(pinNew, "G", "SHA256:good", "feat(pi): signed on top") +
		sigLine(sneaked, "N", "", "fix(nas): sneaked in")
	err, fake, out := e.run(map[string]string{}, true)
	if err == nil {
		t.Fatalf("went ahead\n%s", out)
	}
	for _, want := range []string{"1 of the 2 commits from " + fleet.Short(pinOld) + " to " + fleet.Short(pinNew), fleet.Short(sneaked) + " ✗ unsigned fix(nas): sneaked in"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	changedNothing(t, fake)
}

func TestInventoryPinSaysEveryCommitIsSigned(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.sigs[pinOld+".."+pinNew] = sigLine(pinNew, "G", "SHA256:good", "two") + sigLine("9999999999999999999999999999999999999999", "G", "SHA256:good", "one")
	err, _, out := e.run(map[string]string{}, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "2 commits from " + fleet.Short(pinOld) + " to " + fleet.Short(pinNew) + ", each signed by a key you trust"; !strings.Contains(flat(out), want) {
		t.Errorf("the output lacks %q:\n%s", want, out)
	}
}

func TestInventoryPinAnOlderCommitBringsNothingToCheck(t *testing.T) {
	e := newPinEnv(t, fleet.UpstreamURL)
	e.withFleetManager()
	e.ancestors = map[string]bool{pinNew + " " + pinOld: true}
	// A listing that would be refused if it were read.
	e.sigs[pinNew+".."+pinOld] = sigLine(pinOld, "N", "", "unsigned")
	err, _, out := e.run(map[string]string{}, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "it brings no commit that was not pinned before"; !strings.Contains(flat(out), want) {
		t.Errorf("the output lacks %q:\n%s", want, out)
	}
}

// fmFake answers a history of fleet-manager's main for verifyFleetManagerPin
// on its own: main's newest is fmTip.
type fmFake struct {
	ancestors map[string]bool   // "a b": a is an ancestor of b
	sigs      map[string]string // "base..tip" to the listing
	missing   map[string]bool   // commits the history lacks
	untrusted map[string]bool
	mergeBase string
	logged    []string
}

const (
	fmTip = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fmRec = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fmPin = "cccccccccccccccccccccccccccccccccccccccc"
	fmOld = "dddddddddddddddddddddddddddddddddddddddd"
)

func newFMFake() *fmFake {
	// Everything is on main: an ancestor of its newest commit.
	return &fmFake{ancestors: map[string]bool{fmPin + " " + fmTip: true}, sigs: map[string]string{}, missing: map[string]bool{}, untrusted: map[string]bool{},
		mergeBase: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}
}

func (f *fmFake) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	fail := func() (engine.Result, error) { return engine.Result{Code: 1}, errors.New("exited with code 1") }
	switch {
	case s.Name != "git":
	case strings.Contains(args, "rev-parse --verify -q "):
		ref := strings.TrimSuffix(s.Args[len(s.Args)-1], "^{commit}")
		switch {
		case f.missing[ref]:
			return fail()
		case ref == fmMain:
			return engine.Result{Stdout: fmTip + "\n"}, nil
		}
	case strings.Contains(args, "merge-base --is-ancestor"):
		if !f.ancestors[s.Args[len(s.Args)-2]+" "+s.Args[len(s.Args)-1]] {
			return fail()
		}
	case strings.Contains(args, " merge-base "):
		return engine.Result{Stdout: f.mergeBase + "\n"}, nil
	case strings.Contains(args, " verify-commit "):
		if f.untrusted[s.Args[len(s.Args)-1]] {
			return fail()
		}
	case strings.Contains(args, " log "):
		rng := s.Args[len(s.Args)-1]
		f.logged = append(f.logged, rng)
		return engine.Result{Stdout: f.sigs[rng]}, nil
	}
	return engine.Result{}, nil
}

func (f *fmFake) verify(t *testing.T, rec, pin, base string) (error, string) {
	t.Helper()
	trustCopies(t, fleet.UpstreamURL)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if rec != "" {
		if err := fleet.WriteTrusted(fleet.UpstreamURL, "main", rec); err != nil {
			t.Fatal(err)
		}
	}
	x, _, out := newFakeCtx(f.respond)
	defer releaseFleetManagerHistory(x)
	err := verifyFleetManagerPin(x, pin, base)
	return err, flat(out.String())
}

func TestVerifyFleetManagerPinFromTheRecordWhenThereIsNoBase(t *testing.T) {
	f := newFMFake()
	f.ancestors[fmRec+" "+fmPin] = true
	f.sigs[fmRec+".."+fmPin] = sigLine(fmPin, "G", "SHA256:good", "b") + sigLine(fmTip, "G", "SHA256:good", "a")
	// A base the history lacks is the same as none.
	for _, base := range []string{"", fmOld} {
		f.missing[fmOld] = true
		err, out := f.verify(t, fmRec, fmPin, base)
		if err != nil {
			t.Fatalf("base %q: %v\n%s", base, err, out)
		}
		if want := "2 commits from " + fleet.Short(fmRec) + " to " + fleet.Short(fmPin) + ", each signed by a key you trust"; !strings.Contains(out, want) {
			t.Errorf("base %q: the output lacks %q:\n%s", base, want, out)
		}
	}
	// An unsigned commit in the range is refused.
	f.sigs[fmRec+".."+fmPin] = sigLine(fmPin, "G", "SHA256:good", "b") + sigLine(fmTip, "N", "", "sneaked")
	err, _ := f.verify(t, fmRec, fmPin, "")
	if err == nil || !strings.Contains(err.Error(), "1 of the 2 commits") || !strings.Contains(err.Error(), "✗ unsigned sneaked") {
		t.Errorf("an unsigned commit: %v", err)
	}
}

func TestVerifyFleetManagerPinNeedsARecordWhenThereIsNoBase(t *testing.T) {
	err, _ := newFMFake().verify(t, "", fmPin, "")
	if err == nil || !strings.Contains(err.Error(), "no trusted commit for fleet-manager's main yet: run fleet repo sync fleet-manager, or fleet repo add, first") {
		t.Errorf("err = %v", err)
	}
}

func TestVerifyFleetManagerPinChecksFromTheBase(t *testing.T) {
	f := newFMFake()
	// The base is an ancestor of the pin: base..pin, whatever the record is.
	f.ancestors[fmOld+" "+fmPin] = true
	f.sigs[fmOld+".."+fmPin] = sigLine(fmPin, "G", "SHA256:good", "x")
	if err, out := f.verify(t, "", fmPin, fmOld); err != nil || !strings.Contains(out, "1 commit from "+fleet.Short(fmOld)) {
		t.Errorf("%v\n%s", err, out)
	}
	if len(f.logged) != 1 || f.logged[0] != fmOld+".."+fmPin {
		t.Errorf("listed %v", f.logged)
	}
	// The base and the pin parted: from where they did.
	f = newFMFake()
	f.sigs[f.mergeBase+".."+fmPin] = sigLine(fmPin, "N", "", "off the base")
	if err, _ := f.verify(t, "", fmPin, fmOld); err == nil || !strings.Contains(err.Error(), "off the base") {
		t.Errorf("parted: %v", err)
	}
	if len(f.logged) != 1 || f.logged[0] != f.mergeBase+".."+fmPin {
		t.Errorf("listed %v", f.logged)
	}
}

func TestVerifyFleetManagerPinOlderThanTheBaseBringsNothing(t *testing.T) {
	f := newFMFake()
	f.ancestors[fmPin+" "+fmOld] = true
	err, out := f.verify(t, "", fmPin, fmOld)
	if err != nil || !strings.Contains(out, "an older commit than "+fleet.Short(fmOld)+": it brings no commit that was not pinned before") {
		t.Errorf("%v\n%s", err, out)
	}
	if len(f.logged) != 0 {
		t.Errorf("listed %v", f.logged)
	}
}

// A pin behind the record, or the record itself, has nothing to check beyond
// its own signature: the record's commits were checked as it moved.
func TestVerifyFleetManagerPinBehindTheRecordIsAcceptedWithoutARange(t *testing.T) {
	f := newFMFake()
	f.ancestors[fmPin+" "+fmRec] = true
	f.ancestors[fmRec+" "+fmTip] = true
	err, out := f.verify(t, fmRec, fmPin, "")
	if err != nil || !strings.Contains(out, "behind "+fleet.Short(fmRec)+", the commit you trusted") {
		t.Errorf("%v\n%s", err, out)
	}
	if len(f.logged) != 0 {
		t.Errorf("listed %v", f.logged)
	}
	if err, out := newFMFake().verify(t, fmPin, fmPin, ""); err != nil || !strings.Contains(out, "it is the commit you trusted") {
		t.Errorf("the record itself: %v\n%s", err, out)
	}
}

func TestVerifyFleetManagerPinRefusesWhatCannotBePlacedAgainstTheRecord(t *testing.T) {
	f := newFMFake()
	err, _ := f.verify(t, fmRec, fmPin, "")
	if err == nil || !strings.Contains(err.Error(), fleet.Short(fmRec)) || !strings.Contains(err.Error(), fleet.Short(fmPin)) || !strings.Contains(err.Error(), "gone apart") {
		t.Errorf("gone apart: %v", err)
	}
	f.missing[fmRec] = true
	if err, _ := f.verify(t, fmRec, fmPin, ""); err == nil || !strings.Contains(err.Error(), "not in its history") {
		t.Errorf("record not in the history: %v", err)
	}
}

func TestVerifyFleetManagerPinNeedsASignedCommitOnMain(t *testing.T) {
	f := newFMFake()
	f.untrusted[fmPin] = true
	if err, _ := f.verify(t, fmRec, fmPin, ""); err == nil || !strings.Contains(err.Error(), "is not signed by a key you trust for fleet-manager") {
		t.Errorf("unsigned: %v", err)
	}
	f = newFMFake()
	delete(f.ancestors, fmPin+" "+fmTip)
	if err, _ := f.verify(t, fmRec, fmPin, ""); err == nil || !strings.Contains(err.Error(), "is not on fleet-manager's main") {
		t.Errorf("off main: %v", err)
	}
	f = newFMFake()
	f.missing[fmPin] = true
	if err, _ := f.verify(t, fmRec, fmPin, ""); err == nil || !strings.Contains(err.Error(), "not in the history of fleet-manager's main") {
		t.Errorf("not in the history: %v", err)
	}
}

func TestVerifyFleetManagerPinNeedsYourKeys(t *testing.T) {
	f := newFMFake()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	x, _, _ := newFakeCtx(f.respond)
	err := verifyFleetManagerPin(x, fmPin, "")
	if err == nil || !strings.Contains(err.Error(), "you trust no keys for fleet-manager yet: run fleet repo add (with no address it adds fleet-manager)") {
		t.Errorf("err = %v", err)
	}
}

// With real git: the tools' clone of fleet-manager holds a signed commit, an
// unsigned one, and signed ones on top.
func TestVerifyFleetManagerPinWithRealGit(t *testing.T) {
	w := newRepoWorld(t)
	bare := filepath.Join(w.root, "fm.git")
	work := filepath.Join(w.root, "fm-work")
	for _, args := range [][]string{{"init", "-q", "--bare", "-b", "main", bare}, {"init", "-q", "-b", "main", work}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	w.git(work, "remote", "add", "origin", "file://"+bare)
	commit := func(subject string, signed bool) string {
		args := []string{"commit", "-q", "--allow-empty", "-m", subject}
		if !signed {
			args = append([]string{"-c", "commit.gpgsign=false"}, args...)
		}
		w.git(work, args...)
		return w.git(work, "rev-parse", "HEAD")
	}
	c1 := commit("one", true)
	c2 := commit("sneaked in", false)
	c3 := commit("three", true)
	c4 := commit("four", true)
	w.git(work, "push", "-q", "origin", "main")
	id, _ := fleet.RepoID(fleet.UpstreamURL)
	clone, _ := fleet.ClonePathOfID(id)
	if out, err := exec.Command("git", "clone", "-q", "file://"+bare, clone).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p, _ := fleet.SignersCopy(fleet.UpstreamURL)
	if err := fleet.WriteSignersCopy(p, []byte(w.pub)); err != nil {
		t.Fatal(err)
	}
	verify := func(pin, base string) (error, string) {
		x := w.ctx(engine.Options{Yes: true})
		defer releaseFleetManagerHistory(x)
		err := verifyFleetManagerPin(x, pin, base)
		return err, w.out.String()
	}

	if err := fleet.WriteTrusted(fleet.UpstreamURL, "main", c1); err != nil {
		t.Fatal(err)
	}
	err, _ := verify(c4, "")
	if err == nil || !strings.Contains(err.Error(), "1 of the 3 commits from "+fleet.Short(c1)+" to "+fleet.Short(c4)) || !strings.Contains(err.Error(), fleet.Short(c2)+" ✗ unsigned sneaked in") {
		t.Fatalf("an unsigned commit under signed ones, from the record: %v", err)
	}
	if err, _ := verify(c3, c1); err == nil || !strings.Contains(err.Error(), fleet.Short(c2)+" ✗ unsigned") {
		t.Errorf("an unsigned commit, from a base: %v", err)
	}
	// From a base above the unsigned commit, all is signed.
	err, out := verify(c4, c3)
	if err != nil || !strings.Contains(flat(out), "1 commit from "+fleet.Short(c3)+" to "+fleet.Short(c4)+", each signed by a key you trust") {
		t.Errorf("signed range: %v\n%s", err, out)
	}
	// An older pin than the base brings nothing, and the unsigned commit
	// under it is not looked at.
	err, out = verify(c3, c4)
	if err != nil || !strings.Contains(flat(out), "it brings no commit that was not pinned before") {
		t.Errorf("older pin: %v\n%s", err, out)
	}
	// The record never moves.
	if r := trustedOf(t, fleet.UpstreamURL, "main"); r != c1 {
		t.Errorf("the record moved to %s", r)
	}
}
