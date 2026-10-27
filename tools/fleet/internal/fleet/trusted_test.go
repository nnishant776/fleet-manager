package fleet

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

const testOrigin = "git@github.com:example-org/inventory.git"

// gitRepo is a real repository with origin set to testOrigin, and a state
// folder of its own.
type gitRepo struct {
	t   *testing.T
	dir string
	x   *engine.Ctx
	out *bytes.Buffer
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	g := &gitRepo{t: t, dir: t.TempDir(), out: &bytes.Buffer{}}
	g.x = &engine.Ctx{Context: context.Background(), UI: ui.NewWith(g.out, nil), Runner: engine.Exec{},
		Values: map[string]string{}, LookPath: exec.LookPath}
	g.git("init", "-q", "-b", "main")
	g.git("remote", "add", "origin", testOrigin)
	return g
}

func (g *gitRepo) git(args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", g.dir, "-c", "user.name=t", "-c", "user.email=t@test",
		"-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit adds a commit on the current branch and returns it.
func (g *gitRepo) commit(msg string) string {
	g.t.Helper()
	g.git("commit", "-q", "--allow-empty", "-m", msg)
	return g.git("rev-parse", "HEAD")
}

func (g *gitRepo) check(branch, tip string) error {
	return CheckTrusted(g.x, g.dir, testOrigin, branch, tip)
}

func record(t *testing.T, branch string) string {
	t.Helper()
	r, err := ReadTrusted(testOrigin, branch)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTrustedPathNestsABranchWithASlash(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	for branch, want := range map[string]string{
		"main":         "/state/fleet/repos/github.com/example-org/inventory/trusted/main",
		"host/laptop1": "/state/fleet/repos/github.com/example-org/inventory/trusted/host/laptop1",
		"user/bob":     "/state/fleet/repos/github.com/example-org/inventory/trusted/user/bob",
	} {
		if p, err := TrustedPath("ssh://git@github.com/example-org/inventory", branch); err != nil || p != want {
			t.Errorf("TrustedPath(%q) = %q, %v", branch, p, err)
		}
	}
	for _, bad := range []string{"", "../x", "host//a", "host/.."} {
		if p, err := TrustedPath(testOrigin, bad); err == nil {
			t.Errorf("TrustedPath(%q) = %q, want an error", bad, p)
		}
	}
}

func TestWriteTrustedIsPrivate(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c := strings.Repeat("a", 40)
	if err := WriteTrusted(testOrigin, "host/laptop1", c); err != nil {
		t.Fatal(err)
	}
	p, _ := TrustedPath(testOrigin, "host/laptop1")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Dir(p)); st.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v", st.Mode())
	}
	if data, _ := os.ReadFile(p); string(data) != c+"\n" {
		t.Errorf("content %q", data)
	}
	if r := record(t, "host/laptop1"); r != c {
		t.Errorf("read back %q", r)
	}
	if err := WriteTrusted(testOrigin, "main", "abc"); err == nil {
		t.Error("a short hash was written")
	}
	if err := RemoveTrusted(testOrigin, "host/laptop1"); err != nil || record(t, "host/laptop1") != "" {
		t.Errorf("remove: %v", err)
	}
	if err := RemoveTrusted(testOrigin, "host/laptop1"); err != nil {
		t.Errorf("removing what is not there: %v", err)
	}
}

func TestFirstUseIsAcceptedRecordedAndSaid(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	if err := g.check("main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if r := record(t, "main"); r != c1 {
		t.Errorf("record %q, want %q", r, c1)
	}
	if !strings.Contains(g.out.String(), "no trusted commit") {
		t.Errorf("first use not said:\n%s", g.out)
	}
}

func TestATipThatFollowsTheRecordAdvancesIt(t *testing.T) {
	g := newGitRepo(t)
	g.commit("one")
	if err := g.check("main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := g.check("main", "HEAD"); err != nil {
		t.Fatalf("the same commit: %v", err)
	}
	c3 := func() string { g.commit("two"); return g.commit("three") }()
	if err := g.check("main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if r := record(t, "main"); r != c3 {
		t.Errorf("record %q, want %q", r, c3)
	}
}

func TestARolledBackTipIsRefused(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	c2 := g.commit("two")
	if err := g.check("main", c2); err != nil {
		t.Fatal(err)
	}
	err := g.check("main", c1)
	if err == nil {
		t.Fatal("an older commit was accepted")
	}
	p, _ := TrustedPath(testOrigin, "main")
	for _, want := range []string{"main at " + c1, "does not follow " + c2, "but comes before it", "old commit served again",
		"(git pull)", "rm " + p, "or give the repository a name with fleet repo add and use fleet repo sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if r := record(t, "main"); r != c2 {
		t.Errorf("the record moved to %q", r)
	}
}

func TestARewrittenHistoryIsRefusedAndAcceptedAfterTheRecordIsRemoved(t *testing.T) {
	g := newGitRepo(t)
	g.commit("one")
	c2 := g.commit("two")
	if err := g.check("main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	// The branch is rewritten: a commit that is not on the old line.
	g.git("reset", "-q", "--hard", "HEAD~1")
	rewritten := g.commit("two, differently")
	if err := g.check("main", rewritten); err == nil || !strings.Contains(err.Error(), "does not follow "+c2) {
		t.Fatalf("a rewrite was accepted: %v", err)
	}
	if err := RemoveTrusted(testOrigin, "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.check("main", rewritten); err != nil {
		t.Fatalf("after the record is removed: %v", err)
	}
	if r := record(t, "main"); r != rewritten {
		t.Errorf("record %q", r)
	}
}

func TestTheRecordOfOneBranchIsNotAnothers(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	g.commit("two")
	if err := g.check("host/a", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := g.check("host/b", c1); err != nil {
		t.Fatalf("another branch has its own record: %v", err)
	}
}

func TestAPinnedCommitMayBeOnTheLineBehindOrAhead(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	c2 := g.commit("two")
	c3 := g.commit("three")
	if err := CheckTrustedLine(g.x, g.dir, testOrigin, "main", c2); err != nil {
		t.Fatal(err)
	}
	// Behind the record: accepted, the record stays.
	if err := CheckTrustedLine(g.x, g.dir, testOrigin, "main", c1); err != nil {
		t.Fatalf("an ancestor of the record: %v", err)
	}
	if r := record(t, "main"); r != c2 {
		t.Errorf("record moved back to %q", r)
	}
	// Ahead: accepted, the record advances.
	if err := CheckTrustedLine(g.x, g.dir, testOrigin, "main", c3); err != nil {
		t.Fatalf("a descendant: %v", err)
	}
	if r := record(t, "main"); r != c3 {
		t.Errorf("record %q, want %q", r, c3)
	}
	// Off the line: a side branch from c1.
	g.git("checkout", "-q", "-b", "side", c1)
	side := g.commit("side")
	err := CheckTrustedLine(g.x, g.dir, testOrigin, "main", side)
	if err == nil || !strings.Contains(err.Error(), "does not follow "+c3) {
		t.Errorf("a commit off the line: %v", err)
	}
	// A followed branch (not pinned) refuses an older commit.
	if err := CheckTrusted(g.x, g.dir, testOrigin, "main", c1); err == nil {
		t.Error("CheckTrusted accepted an ancestor of the record")
	}
}

func TestAShallowCloneSaysItCannotTell(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	g.commit("two")
	g.commit("three")
	if err := g.check("main", c1); err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--depth=1", "file://"+g.dir, clone).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	err := CheckTrusted(g.x, clone, testOrigin, "main", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "shallow clone") || !strings.Contains(err.Error(), "--unshallow") {
		t.Errorf("a shallow clone: %v", err)
	}
	if r := record(t, "main"); r != c1 {
		t.Errorf("the record moved to %q", r)
	}
	if out, err := exec.Command("git", "-C", clone, "fetch", "-q", "--unshallow").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := CheckTrusted(g.x, clone, testOrigin, "main", "HEAD"); err != nil {
		t.Errorf("after the rest is fetched: %v", err)
	}
}

func TestAdvanceAndForgetFollowAPush(t *testing.T) {
	g := newGitRepo(t)
	g.git("checkout", "-q", "-b", "host/h")
	c1 := g.commit("one")
	if err := AdvanceTrusted(g.x, g.dir, "host/h"); err != nil {
		t.Fatal(err)
	}
	if r := record(t, "host/h"); r != c1 {
		t.Errorf("record %q, want %q", r, c1)
	}
	if err := ForgetTrusted(g.x, g.dir, "host/h"); err != nil || record(t, "host/h") != "" {
		t.Errorf("forget: %v", err)
	}
	g.x.Options.DryRun = true
	if err := AdvanceTrusted(g.x, g.dir, "host/h"); err != nil || record(t, "host/h") != "" {
		t.Errorf("a dry run wrote a record: %v", err)
	}
}

func TestTheRefusalOfARepositoryWithANameSaysToUseRepoSync(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("one")
	c2 := g.commit("two")
	if err := g.check("main", c2); err != nil {
		t.Fatal(err)
	}
	id, _ := RepoID(testOrigin)
	if err := SetName("inventory", id); err != nil {
		t.Fatal(err)
	}
	err := g.check("main", c1)
	if err == nil {
		t.Fatal("an older commit was accepted")
	}
	for _, want := range []string{"does not follow " + c2 + ", the commit you trusted last",
		"review and accept it with fleet repo sync inventory --accept-rewrite main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "rm ") {
		t.Errorf("a named repository is told to remove a file:\n%v", err)
	}
}
