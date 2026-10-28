package fleet

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// otherKeyCommit adds a commit to work's main signed by a second key, which
// the signers file does not list, and returns it and the key's fingerprint.
func (f *forge) otherKeyCommit(msg string) (commit, fingerprint string) {
	f.t.Helper()
	key := filepath.Join(f.root, "other")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "o@test", "-f", key).CombinedOutput(); err != nil {
		f.t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	f.run("", "-C", f.work, "-c", "user.name=t", "-c", "user.email=t@test", "-c", "gpg.format=ssh",
		"-c", "user.signingKey="+key+".pub", "-c", "commit.gpgsign=true", "commit", "-q", "--allow-empty", "-m", msg)
	out := f.run("", "-C", f.work, "-c", "gpg.ssh.allowedSignersFile="+filepath.Join(f.root, "allowed_signers"), "log", "-1", "--format=%GK")
	return f.run("", "-C", f.work, "rev-parse", "HEAD"), out
}

func TestUnverifiedCommitsASignedRangeHasNone(t *testing.T) {
	f := newForge(t)
	base := f.git(f.work, "rev-parse", "HEAD")
	f.commit("two")
	f.commit("three")
	tip := f.git(f.work, "rev-parse", "HEAD")
	signers := filepath.Join(f.root, "allowed_signers")
	bad, total, err := RangeSignatures(f.x, f.work, signers, base, tip)
	if err != nil || len(bad) != 0 || total != 2 {
		t.Errorf("bad %v, total %d, err %v", bad, total, err)
	}
	if n, err := CountCommits(f.x, f.work, base, tip); err != nil || n != 2 {
		t.Errorf("CountCommits = %d, %v", n, err)
	}
}

func TestUnverifiedCommitsListsAnUnsignedOneUnderASignedTip(t *testing.T) {
	f := newForge(t)
	base := f.git(f.work, "rev-parse", "HEAD")
	f.commit("two")
	mid := f.unsigned("sneaked in")
	f.commit("four")
	tip := f.git(f.work, "rev-parse", "HEAD")
	// The repository's own setting, as the clones have it.
	f.git(f.work, "config", "gpg.ssh.allowedSignersFile", filepath.Join(f.root, "allowed_signers"))
	bad, err := UnverifiedCommits(f.x, f.work, "", base, tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0] != short(mid)+" ✗ unsigned sneaked in" {
		t.Errorf("bad = %q", bad)
	}
}

func TestUnverifiedCommitsListsACommitByAnUnknownKeyWithItsFingerprint(t *testing.T) {
	f := newForge(t)
	base := f.git(f.work, "rev-parse", "HEAD")
	c, fp := f.otherKeyCommit("by someone else")
	f.commit("on top")
	tip := f.git(f.work, "rev-parse", "HEAD")
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("no fingerprint for the other key: %q", fp)
	}
	bad, err := UnverifiedCommits(f.x, f.work, filepath.Join(f.root, "allowed_signers"), base, tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0] != short(c)+" ✗ "+fp+" by someone else" {
		t.Errorf("bad = %q, want the fingerprint %s", bad, fp)
	}
}

// A signed tip over an unsigned commit is refused, and nothing is recorded or
// moved; the message names the command to review it with.
func TestSyncCloneRefusesAnUnsignedCommitUnderASignedTip(t *testing.T) {
	f := newForge(t)
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	one := f.record("main")
	bad := f.unsigned("sneaked in")
	f.commit("two")
	f.git(f.work, "push", "-q", "origin", "main")
	err := f.sync()
	if err == nil {
		t.Fatalf("went ahead\n%s", f.out)
	}
	for _, want := range []string{"1 of the commits on main since " + short(one), short(bad) + " ✗ unsigned sneaked in", "Review them with fleet repo sync "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q: %v", want, err)
		}
	}
	if f.record("main") != one || f.head() != one || !f.detached() {
		t.Errorf("record %q, head %q, want both %q", f.record("main"), f.head(), one)
	}
}
