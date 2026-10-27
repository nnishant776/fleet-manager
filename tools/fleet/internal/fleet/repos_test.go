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

func TestDataDirAndClonePath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if d, err := DataDir(); err != nil || d != "/data/fleet" {
		t.Errorf("DataDir = %q, %v", d, err)
	}
	p, err := ClonePath("git@github.com:example-org/inventory.git")
	if err != nil || p != "/data/fleet/repos/github.com/example-org/inventory" {
		t.Errorf("ClonePath = %q, %v", p, err)
	}
	if _, err := ClonePath("nonsense"); err == nil {
		t.Error("ClonePath of a non-address succeeded")
	}
	// Unset, or not absolute: under the home folder.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"", "relative"} {
		t.Setenv("XDG_DATA_HOME", v)
		if d, _ := DataDir(); d != filepath.Join(home, ".local", "share", "fleet") {
			t.Errorf("XDG_DATA_HOME=%q: DataDir = %q", v, d)
		}
	}
}

func TestNamesFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if names, err := ReadNames(); err != nil || len(names) != 0 {
		t.Fatalf("no file: %v, %v", names, err)
	}
	if err := SetName("inventory", "github.com/example-org/inventory"); err != nil {
		t.Fatal(err)
	}
	if err := SetName("secrets", "github.com/example-org/secrets"); err != nil {
		t.Fatal(err)
	}
	p, _ := namesPath()
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Dir(p)); st.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v", st.Mode())
	}
	// Another repository may not take a name.
	err := SetName("inventory", "github.com/example-org/other")
	if err == nil || !strings.Contains(err.Error(), "inventory already names github.com/example-org/inventory") {
		t.Errorf("a taken name: %v", err)
	}
	// The same pair again is fine; a new name replaces the old one.
	if err := SetName("inventory", "github.com/example-org/inventory"); err != nil {
		t.Errorf("again: %v", err)
	}
	if err := SetName("inv", "github.com/example-org/inventory"); err != nil {
		t.Fatal(err)
	}
	names, _ := ReadNames()
	if len(names) != 2 || names["inv"] != "github.com/example-org/inventory" || names["inventory"] != "" {
		t.Errorf("names = %v", names)
	}
	if NameOf("github.com/example-org/inventory") != "inv" || NameOf("github.com/example-org/none") != "" {
		t.Error("NameOf")
	}
	for _, bad := range []string{"", "Upper", "-x", ".x", "a b", "a/b"} {
		if err := SetName(bad, "github.com/example-org/x"); err == nil {
			t.Errorf("SetName(%q) succeeded", bad)
		}
	}
}

func TestResolveName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "/data")
	if err := SetName("inventory", "github.com/example-org/inventory"); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.Mkdir(filepath.Join(cwd, "plainfolder"), 0o755); err != nil {
		t.Fatal(err)
	}
	x := &engine.Ctx{Values: map[string]string{}}
	for _, same := range []string{"", ".", "./x", "../x", "a/b", "/etc/nixos", "~/x", "file:///tmp/r", "git@github.com:example-org/r.git",
		"github:example-org/r", "plainfolder"} {
		got, err := ResolveName(x, "flake", same)
		if err != nil || got != same {
			t.Errorf("ResolveName(%q) = %q, %v", same, got, err)
		}
	}
	if len(x.Values) != 0 {
		t.Errorf("values set for paths: %v", x.Values)
	}
	got, err := ResolveName(x, "flake", "inventory")
	if err != nil || got != "/data/fleet/repos/github.com/example-org/inventory" {
		t.Errorf("a name: %q, %v", got, err)
	}
	if x.Values["flake_name"] != "inventory" {
		t.Errorf("values = %v", x.Values)
	}
	_, err = ResolveName(x, "flake", "nothing")
	if err == nil || !strings.Contains(err.Error(), `"nothing" is neither a path nor the name of a repository: fleet repo list shows the names, fleet repo add makes one`) {
		t.Errorf("unknown word: %v", err)
	}
}

// forge is a bare origin that a work repository pushes signed commits to,
// and the tools' clone of it, set up like fleet repo add does.
type forge struct {
	t       *testing.T
	root    string
	origin  string // the bare repository
	address string
	work    string
	clone   string
	key     string
	x       *engine.Ctx
	out     *bytes.Buffer
}

func newForge(t *testing.T) *forge {
	t.Helper()
	for _, tool := range []string{"git", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f := &forge{t: t, root: t.TempDir(), out: &bytes.Buffer{}}
	f.origin = filepath.Join(f.root, "origin.git")
	f.address = "file://" + f.origin
	f.work = filepath.Join(f.root, "work")
	f.clone = filepath.Join(f.root, "clone")
	f.key = filepath.Join(f.root, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t@test", "-f", f.key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(f.key + ".pub")
	pf := strings.Fields(string(pub))
	signers := filepath.Join(f.root, "allowed_signers")
	if err := os.WriteFile(signers, []byte("t@test namespaces=\"git\" "+pf[0]+" "+pf[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.x = &engine.Ctx{Context: context.Background(), UI: ui.NewWith(f.out, nil), Runner: engine.Exec{},
		Options: engine.Options{Yes: true}, Values: map[string]string{}, LookPath: exec.LookPath}
	f.run("", "init", "-q", "--bare", "-b", "main", f.origin)
	f.run("", "init", "-q", "-b", "main", f.work)
	f.git(f.work, "remote", "add", "origin", f.address)
	f.commit("one")
	f.git(f.work, "push", "-q", "origin", "main")
	f.run("", "clone", "-q", "--no-checkout", f.address, f.clone)
	f.git(f.clone, "config", "gpg.ssh.allowedSignersFile", signers)
	f.git(f.clone, "checkout", "-q", "--detach", "refs/remotes/origin/main")
	return f
}

func (f *forge) run(dir string, args ...string) string {
	f.t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// git runs git in dir, signing as the test key.
func (f *forge) git(dir string, args ...string) string {
	f.t.Helper()
	cfg := []string{"-c", "user.name=t", "-c", "user.email=t@test", "-c", "gpg.format=ssh", "-c", "user.signingKey=" + f.key + ".pub",
		"-c", "commit.gpgsign=true"}
	return f.run("", append(append([]string{"-C", dir}, cfg...), args...)...)
}

// commit adds a signed commit to work's main and returns it.
func (f *forge) commit(msg string) string {
	f.t.Helper()
	f.git(f.work, "commit", "-q", "--allow-empty", "-m", msg)
	return f.git(f.work, "rev-parse", "HEAD")
}

func (f *forge) unsigned(msg string) string {
	f.t.Helper()
	f.git(f.work, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", msg)
	return f.git(f.work, "rev-parse", "HEAD")
}

func (f *forge) sync() error { return SyncClone(f.x, f.clone) }

func (f *forge) head() string { return f.git(f.clone, "rev-parse", "HEAD") }

func (f *forge) detached() bool {
	return exec.Command("git", "-C", f.clone, "symbolic-ref", "-q", "HEAD").Run() != nil
}

func (f *forge) record(branch string) string {
	f.t.Helper()
	r, err := ReadTrusted(f.address, branch)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestSyncCloneRecordsMainAndDetachesAtIt(t *testing.T) {
	f := newForge(t)
	one := f.git(f.work, "rev-parse", "HEAD")
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	if f.record("main") != one || f.head() != one || !f.detached() {
		t.Errorf("record %q, head %q, detached %v\n%s", f.record("main"), f.head(), f.detached(), f.out)
	}
}

func TestSyncCloneTakesANewSignedCommit(t *testing.T) {
	f := newForge(t)
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	two := f.commit("two")
	f.git(f.work, "push", "-q", "origin", "main")
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	if f.record("main") != two || f.head() != two || !f.detached() {
		t.Errorf("record %q, head %q\n%s", f.record("main"), f.head(), f.out)
	}
	if !strings.Contains(f.out.String(), "the clone is at "+short(two)) {
		t.Errorf("output lacks the commit:\n%s", f.out)
	}
}

func TestSyncCloneRefusesAnOlderCommitServedAgain(t *testing.T) {
	f := newForge(t)
	one := f.git(f.work, "rev-parse", "HEAD")
	f.commit("two")
	f.git(f.work, "push", "-q", "origin", "main")
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	two := f.record("main")
	f.git(f.work, "push", "-q", "-f", "origin", one+":refs/heads/main")
	err := f.sync()
	if err == nil || !strings.Contains(err.Error(), "does not follow") {
		t.Fatalf("err = %v\n%s", err, f.out)
	}
	if f.record("main") != two || f.head() != two {
		t.Errorf("record %q, head %q, want both %q", f.record("main"), f.head(), two)
	}
}

func TestSyncCloneRefusesAnUnsignedTip(t *testing.T) {
	f := newForge(t)
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	one := f.record("main")
	f.unsigned("two")
	f.git(f.work, "push", "-q", "origin", "main")
	err := f.sync()
	if err == nil || !strings.Contains(err.Error(), "gpg.ssh.allowedSignersFile") {
		t.Fatalf("err = %v\n%s", err, f.out)
	}
	if f.record("main") != one || f.head() != one {
		t.Errorf("record %q, head %q, want both %q", f.record("main"), f.head(), one)
	}
}

func TestSyncCloneMovesALaggingBranchAndLeavesOneAhead(t *testing.T) {
	f := newForge(t)
	one := f.git(f.work, "rev-parse", "HEAD")
	f.git(f.work, "push", "-q", "origin", "main:refs/heads/lagging", "main:refs/heads/ahead")
	f.run("", "-C", f.clone, "fetch", "-q", "origin")
	f.git(f.clone, "branch", "lagging", one)
	f.git(f.clone, "checkout", "-q", "-b", "ahead", "origin/ahead")
	f.git(f.clone, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "local")
	aheadLocal := f.git(f.clone, "rev-parse", "HEAD")
	f.git(f.clone, "branch", "onlyhere", one)
	f.git(f.clone, "checkout", "-q", "--detach", one)
	two := f.commit("two")
	f.git(f.work, "push", "-q", "origin", "main", "main:refs/heads/lagging")
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	if got := f.git(f.clone, "rev-parse", "lagging"); got != two {
		t.Errorf("lagging is at %s, want %s", got, two)
	}
	if got := f.git(f.clone, "rev-parse", "ahead"); got != aheadLocal {
		t.Errorf("ahead moved to %s", got)
	}
	if got := f.git(f.clone, "rev-parse", "onlyhere"); got != one {
		t.Errorf("a branch origin lacks moved to %s", got)
	}
	out := strings.Join(strings.Fields(f.out.String()), " ") // the output wraps long lines
	for _, want := range []string{
		"moved lagging to origin's " + short(two),
		"ahead has commits not pushed: git -C " + f.clone + " push origin ahead",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "onlyhere") {
		t.Errorf("a word about the branch origin lacks:\n%s", out)
	}
}

func TestSyncCloneDryRunChangesNothing(t *testing.T) {
	f := newForge(t)
	f.x.Options.DryRun = true
	if err := f.sync(); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	if f.record("main") != "" {
		t.Errorf("a record was written: %q", f.record("main"))
	}
}
