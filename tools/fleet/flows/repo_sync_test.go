package flows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// otherKey makes a second signing key; its allowed_signers line is returned.
func (w *repoWorld) otherKey(name string) (key, line string) {
	w.t.Helper()
	key = filepath.Join(w.root, name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name+"@test", "-f", key).CombinedOutput(); err != nil {
		w.t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(key + ".pub")
	f := strings.Fields(string(data))
	return key, name + "@test namespaces=\"git\" " + f[0] + " " + f[1] + "\n"
}

// gitAs is w.git, signing with the given key, or not at all when it is "".
func (w *repoWorld) gitAs(key, dir string, args ...string) string {
	w.t.Helper()
	cfg := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@test"}
	if key != "" {
		cfg = append(cfg, "-c", "gpg.format=ssh", "-c", "user.signingKey="+key+".pub", "-c", "commit.gpgsign=true")
	}
	out, err := exec.Command("git", append(cfg, args...)...).CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// push commits on a branch of the work folder, signed by key, and pushes it.
func (w *repoWorld) push(key, work, branch, subject string) string {
	w.t.Helper()
	w.git(work, "switch", "-q", branch)
	w.gitAs(key, work, "commit", "-q", "--allow-empty", "-m", subject)
	w.git(work, "push", "-q", "-f", "origin", branch)
	tip := w.git(work, "rev-parse", "HEAD")
	w.git(work, "switch", "-q", "main")
	return tip
}

// sync runs fleet repo sync; typed is what a person at a terminal types.
func (w *repoWorld) sync(opts engine.Options, typed, names, rewrite string) error {
	x := w.ctx(opts)
	if typed != "" {
		x.UI = ui.NewScripted(w.out, strings.NewReader(typed))
	}
	return engine.RunWith(x, RepoSync, map[string]string{"name": names, "accept-rewrite": rewrite})
}

// synced sets up an inventory with its own keys, added and cloned.
func syncedWorld(t *testing.T) (w *repoWorld, address, work, clone string) {
	t.Helper()
	w = newRepoWorld(t)
	address, work = w.origin("inventory", true)
	if err := w.add(address, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	clone, _ = fleet.ClonePath(address)
	return
}

func recordOf(t *testing.T, address, branch string) string {
	t.Helper()
	rec, err := fleet.ReadTrusted(address, branch)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func wantOut(t *testing.T, w *repoWorld, wants ...string) {
	t.Helper()
	flat := strings.Join(strings.Fields(w.out.String()), " ") // notes wrap
	for _, want := range wants {
		if !strings.Contains(flat, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("the output lacks %q:\n%s", want, w.out)
		}
	}
}

func TestRepoSyncShowsNewSignedCommitsAndAcceptsThem(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	oldMain := recordOf(t, address, "main")
	mainTip := w.push(w.key, work, "main", "a change to main")
	nasTip := w.push(w.key, work, "host/nas", "a change to nas")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	wantOut(t, w, "a change to main", "a change to nas", "✓ t@test SHA256:", "follows "+oldMain[:12], "inventory: accepted")
	if got := recordOf(t, address, "main"); got != mainTip {
		t.Errorf("main record = %s, want %s", got, mainTip)
	}
	if got := recordOf(t, address, "host/nas"); got != nasTip {
		t.Errorf("host/nas record = %s, want %s", got, nasTip)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != mainTip {
		t.Errorf("the clone is at %s, want %s", got, mainTip)
	}
	// Again: nothing new, nothing asked.
	if err := w.sync(engine.Options{}, "", "inventory", ""); err != nil {
		t.Fatalf("again: %v\n%s", err, w.out)
	}
	wantOut(t, w, "inventory: up to date")
	if strings.Contains(w.out.String(), "Accept these") {
		t.Errorf("asked with nothing new:\n%s", w.out)
	}
}

func TestRepoSyncAsksBeforeWritingAndStopsOnNo(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	oldMain := recordOf(t, address, "main")
	w.push(w.key, work, "main", "a change to main")
	if err := w.sync(engine.Options{}, "n\n", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	wantOut(t, w, "Accept these for inventory?", "left as is, because you said no")
	if got := recordOf(t, address, "main"); got != oldMain {
		t.Errorf("the record moved to %s", got)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != oldMain {
		t.Errorf("the clone moved to %s", got)
	}
	if err := w.sync(engine.Options{}, "y\n", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "main"); got == oldMain {
		t.Error("the record did not move after yes")
	}
}

func TestRepoSyncDryRunWritesNothing(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	oldMain := recordOf(t, address, "main")
	w.push(w.key, work, "main", "a change to main")
	if err := w.sync(engine.Options{DryRun: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	wantOut(t, w, "a change to main", "dry run")
	if got := recordOf(t, address, "main"); got != oldMain {
		t.Errorf("the record moved to %s in a dry run", got)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != oldMain {
		t.Errorf("the clone moved to %s in a dry run", got)
	}
}

func TestRepoSyncRefusesAMainThatWentBackwards(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	first := recordOf(t, address, "main")
	w.push(w.key, work, "main", "second")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	second := recordOf(t, address, "main")
	// Origin's main is put back to the first commit, and host/nas moves on.
	w.git(work, "reset", "-q", "--hard", first)
	w.git(work, "push", "-q", "-f", "origin", "main")
	w.push(w.key, work, "host/nas", "nas moves on")
	err := w.sync(engine.Options{Yes: true}, "", "", "")
	if err == nil {
		t.Fatalf("accepted:\n%s", w.out)
	}
	wantOut(t, w, "main does not follow "+second[:12], "--accept-rewrite main", "inventory: refused")
	if got := recordOf(t, address, "main"); got != second {
		t.Errorf("the record of main = %s", got)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != second {
		t.Errorf("the clone moved to %s", got)
	}
	// Nothing written for host/nas either: the repository was refused whole.
	nasNow := gitOut(t, work, "rev-parse", "host/nas")
	if got := recordOf(t, address, "host/nas"); got == nasNow {
		t.Error("host/nas was recorded although the repository was refused")
	}
}

func TestRepoSyncAcceptRewriteNeedsATerminalAndTheTypedName(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	first := recordOf(t, address, "main")
	w.push(w.key, work, "main", "the dropped commit")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	dropped := recordOf(t, address, "main")
	w.git(work, "reset", "-q", "--hard", first)
	rewritten := w.push(w.key, work, "main", "the rewritten commit")

	// One repository name is needed.
	if err := w.sync(engine.Options{Yes: true}, "", "", "main"); err == nil {
		t.Error("accepted a rewrite with no repository named")
	}
	wantOut(t, w, "exactly one repository name")
	// No terminal: refused, even with --yes.
	err := w.sync(engine.Options{Yes: true}, "", "inventory", "main")
	if err == nil {
		t.Fatalf("accepted without a terminal:\n%s", w.out)
	}
	wantOut(t, w, "needs you at a terminal to type the branch's name", "the dropped commit", "the rewritten commit")
	if got := recordOf(t, address, "main"); got != dropped {
		t.Errorf("the record moved to %s", got)
	}
	// A wrong word: not accepted, and not a refusal.
	if err := w.sync(engine.Options{Yes: true}, "yes\n", "inventory", "main"); err != nil {
		t.Fatalf("wrong word: %v\n%s", err, w.out)
	}
	wantOut(t, w, "(type main)", "left as is")
	if got := recordOf(t, address, "main"); got != dropped {
		t.Errorf("a wrong word moved the record to %s", got)
	}
	// The branch's name: accepted.
	if err := w.sync(engine.Options{Yes: true}, "main\n", "inventory", "main"); err != nil {
		t.Fatalf("typed: %v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "main"); got != rewritten {
		t.Errorf("record = %s, want %s", got, rewritten)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != rewritten {
		t.Errorf("the clone is at %s", got)
	}
	// A branch that follows is taken as usual, with a note on the flag.
	w.push(w.key, work, "main", "after")
	if err := w.sync(engine.Options{Yes: true}, "", "inventory", "main"); err != nil {
		t.Fatalf("follows: %v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "main"); got == rewritten {
		t.Error("main did not move on")
	}
}

func TestRepoSyncRefusesAnUnsignedHostTipAndWritesNothing(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	oldMain, oldNas := recordOf(t, address, "main"), recordOf(t, address, "host/nas")
	w.push(w.key, work, "main", "signed change to main")
	w.push("", work, "host/nas", "unsigned change to nas")
	err := w.sync(engine.Options{Yes: true}, "", "", "")
	if err == nil {
		t.Fatalf("accepted:\n%s", w.out)
	}
	wantOut(t, w, "unsigned", "host/nas's newest commit", "is not signed by a key you trust", "inventory: refused")
	if got := recordOf(t, address, "main"); got != oldMain {
		t.Errorf("main was recorded: %s", got)
	}
	if got := recordOf(t, address, "host/nas"); got != oldNas {
		t.Errorf("host/nas was recorded: %s", got)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != oldMain {
		t.Errorf("the clone moved to %s", got)
	}
}

func TestRepoSyncShowsAnUnknownKeyBySomethingOtherThanAGoodSignature(t *testing.T) {
	w, _, work, _ := syncedWorld(t)
	key2, _ := w.otherKey("mallory")
	w.push(key2, work, "main", "signed by a stranger")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err == nil {
		t.Fatalf("accepted:\n%s", w.out)
	}
	out := w.out.String()
	if !strings.Contains(out, "signed by a stranger") || !strings.Contains(out, "✗ SHA256:") || strings.Contains(out, "✓ mallory") {
		t.Errorf("a stranger's commit:\n%s", out)
	}
}

func TestRepoSyncLeavesAUserBranchSignedByTheUsersOwnKey(t *testing.T) {
	w, address, work, _ := syncedWorld(t)
	bob, _ := w.otherKey("bob")
	w.git(work, "switch", "-q", "-c", "user/bob")
	w.gitAs(bob, work, "commit", "-q", "--allow-empty", "-m", "bob's change")
	w.git(work, "push", "-q", "origin", "user/bob")
	w.git(work, "switch", "-q", "main")
	mainTip := w.push(w.key, work, "main", "a change to main")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	wantOut(t, w, "bob's change", "left: user/bob is signed by SHA256:", "which is not one of your keys", "bob's own key", "inventory: accepted")
	if got := recordOf(t, address, "user/bob"); got != "" {
		t.Errorf("user/bob was recorded: %s", got)
	}
	if got := recordOf(t, address, "main"); got != mainTip {
		t.Errorf("main = %s", got)
	}

	// A user's branch with a record that it no longer follows: refused, unless
	// the rewrite is accepted, which drops the record.
	if err := fleet.WriteTrusted(address, "user/bob", mainTip); err != nil {
		t.Fatal(err)
	}
	err := w.sync(engine.Options{Yes: true}, "", "", "")
	if err == nil || !strings.Contains(w.out.String(), "--accept-rewrite user/bob") {
		t.Fatalf("not refused: %v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "user/bob"); got != mainTip {
		t.Errorf("the record changed: %s", got)
	}
	if err := w.sync(engine.Options{Yes: true}, "user/bob\n", "inventory", "user/bob"); err != nil {
		t.Fatalf("accepted rewrite: %v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "user/bob"); got != "" {
		t.Errorf("the record of user/bob stayed: %s", got)
	}
}

func TestRepoSyncTakesAUserBranchSignedByYourKey(t *testing.T) {
	w, address, work, _ := syncedWorld(t)
	w.git(work, "switch", "-q", "-c", "user/amy")
	w.git(work, "commit", "-q", "--allow-empty", "-m", "amy's change")
	w.git(work, "push", "-q", "origin", "user/amy")
	tip := w.git(work, "rev-parse", "HEAD")
	w.git(work, "switch", "-q", "main")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "user/amy"); got != tip {
		t.Errorf("user/amy = %s, want %s", got, tip)
	}
}

func TestRepoSyncForgetsABranchOriginDeleted(t *testing.T) {
	w, address, work, _ := syncedWorld(t)
	w.git(work, "push", "-q", "origin", "--delete", "host/nas")
	if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	wantOut(t, w, "host/nas is gone from origin", "inventory: accepted")
	if got := recordOf(t, address, "host/nas"); got != "" {
		t.Errorf("the record stayed: %s", got)
	}
}

func TestRepoSyncTakesNewKeysOnlyFromACommitSignedByAKeyYouTrust(t *testing.T) {
	t.Run("a key added by a trusted key", func(t *testing.T) {
		w, address, work, _ := syncedWorld(t)
		key2, line2 := w.otherKey("second")
		signersFile := filepath.Join(work, "fleet", "allowed_signers")
		if err := os.WriteFile(signersFile, []byte(w.pub+line2), 0o644); err != nil {
			t.Fatal(err)
		}
		w.git(work, "add", "fleet/allowed_signers")
		w.git(work, "commit", "-q", "-m", "add the second key")
		w.git(work, "push", "-q", "origin", "main")
		tip := w.push(key2, work, "main", "signed by the second key")
		if err := w.sync(engine.Options{Yes: true}, "", "", ""); err != nil {
			t.Fatalf("%v\n%s", err, w.out)
		}
		wantOut(t, w, "+ second@test", "is signed by a key you trust", "inventory: accepted")
		copyPath, _ := fleet.SignersCopy(address)
		if data, _ := os.ReadFile(copyPath); string(data) != w.pub+line2 {
			t.Errorf("the copy is %q", data)
		}
		if got := recordOf(t, address, "main"); got != tip {
			t.Errorf("main = %s", got)
		}
	})
	t.Run("a key that adds itself", func(t *testing.T) {
		w, address, work, clone := syncedWorld(t)
		oldMain := recordOf(t, address, "main")
		key2, line2 := w.otherKey("second")
		if err := os.WriteFile(filepath.Join(work, "fleet", "allowed_signers"), []byte(w.pub+line2), 0o644); err != nil {
			t.Fatal(err)
		}
		w.git(work, "add", "fleet/allowed_signers")
		w.gitAs(key2, work, "commit", "-q", "-m", "add myself")
		w.git(work, "push", "-q", "origin", "main")
		err := w.sync(engine.Options{Yes: true}, "", "", "")
		if err == nil {
			t.Fatalf("accepted:\n%s", w.out)
		}
		wantOut(t, w, "its keys changed in ", "which no key you trust signed", "inventory: refused")
		copyPath, _ := fleet.SignersCopy(address)
		if data, _ := os.ReadFile(copyPath); string(data) != w.pub {
			t.Errorf("the copy changed to %q", data)
		}
		if got := recordOf(t, address, "main"); got != oldMain {
			t.Errorf("main = %s", got)
		}
		if got := gitOut(t, clone, "rev-parse", "HEAD"); got != oldMain {
			t.Errorf("the clone moved to %s", got)
		}
	})
}

func TestRepoSyncChecksTheRepositoryWithKeysFirstThenTheOnesThatBorrowThem(t *testing.T) {
	w := newRepoWorld(t)
	inv, invWork := w.origin("inventory", true)
	sec, secWork := w.origin("secrets", false)
	if err := w.add(inv, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if err := w.add(sec, map[string]string{"signers-from": "inventory"}); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	// The inventory adds a key, and the secrets repository's newest commit is
	// signed by it: the keys must be taken first.
	key2, line2 := w.otherKey("second")
	if err := os.WriteFile(filepath.Join(invWork, "fleet", "allowed_signers"), []byte(w.pub+line2), 0o644); err != nil {
		t.Fatal(err)
	}
	w.git(invWork, "add", "fleet/allowed_signers")
	w.git(invWork, "commit", "-q", "-m", "add the second key")
	w.git(invWork, "push", "-q", "origin", "main")
	secTip := w.push(key2, secWork, "main", "secrets, by the second key")

	// Named secrets first: the order of the run is the keys' repository first.
	if err := w.sync(engine.Options{Yes: true}, "", "secrets\ninventory", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	out := w.out.String()
	if strings.Index(out, "• inventory") > strings.Index(out, "• secrets") {
		t.Errorf("secrets came before inventory:\n%s", out)
	}
	wantOut(t, w, "inventory: accepted", "secrets: accepted")
	if got := recordOf(t, sec, "main"); got != secTip {
		t.Errorf("secrets main = %s, want %s", got, secTip)
	}
	// Without the keys' repository in the run, the secrets are checked against
	// the copy as it stands.
	w.push(key2, secWork, "main", "again")
	if err := w.sync(engine.Options{Yes: true}, "", "secrets", ""); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
}

func TestRepoSyncRefusesAnUnknownNameAndAMissingClone(t *testing.T) {
	w, _, _, clone := syncedWorld(t)
	if err := w.sync(engine.Options{Yes: true}, "", "nothing", ""); err == nil {
		t.Error("an unknown name was accepted")
	}
	wantOut(t, w, `"nothing" is not the name of a repository`)
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	err := w.sync(engine.Options{Yes: true}, "", "", "")
	if err == nil {
		t.Fatalf("accepted:\n%s", w.out)
	}
	wantOut(t, w, "run fleet repo add again")
}

// A signed tip over an unsigned commit: the tip's signature does not vouch for
// the commit under it. Refused under --yes and without a terminal; at one, the
// branch's name typed accepts, and a wrong word leaves everything as it is.
func TestRepoSyncUnsignedCommitUnderASignedTip(t *testing.T) {
	w, address, work, clone := syncedWorld(t)
	oldMain := recordOf(t, address, "main")
	w.push("", work, "main", "sneaked in")
	tip := w.push(w.key, work, "main", "signed on top")

	err := w.sync(engine.Options{Yes: true}, "", "", "")
	if err == nil {
		t.Fatalf("accepted under --yes:\n%s", w.out)
	}
	wantOut(t, w, "sneaked in", "✗ unsigned", "main has commits not signed by a key you trust", "inventory: refused")
	if got := recordOf(t, address, "main"); got != oldMain {
		t.Errorf("the record moved to %s", got)
	}
	// No terminal, no --yes.
	if err := w.sync(engine.Options{}, "", "", ""); err == nil {
		t.Fatalf("accepted without a terminal:\n%s", w.out)
	}
	// A wrong word: left as is, not a refusal.
	if err := w.sync(engine.Options{}, "nope\n", "", ""); err != nil {
		t.Fatalf("wrong word: %v\n%s", err, w.out)
	}
	wantOut(t, w, "(type main)", "left as is, because you did not type the branch's name")
	if got := recordOf(t, address, "main"); got != oldMain {
		t.Errorf("a wrong word moved the record to %s", got)
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != oldMain {
		t.Errorf("the clone moved to %s", got)
	}
	// The branch's name, then yes to the usual question.
	if err := w.sync(engine.Options{}, "main\ny\n", "", ""); err != nil {
		t.Fatalf("typed: %v\n%s", err, w.out)
	}
	if got := recordOf(t, address, "main"); got != tip {
		t.Errorf("record = %s, want %s", got, tip)
	}
}
