package flows

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// repoWorld is a signing key, and file:// origins that the key signs for,
// with real git; state and data folders of its own.
type repoWorld struct {
	t    *testing.T
	root string
	key  string
	pub  string // the key's allowed_signers line
	out  *bytes.Buffer
}

func newRepoWorld(t *testing.T) *repoWorld {
	t.Helper()
	for _, tool := range []string{"git", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	w := &repoWorld{t: t, root: t.TempDir(), out: &bytes.Buffer{}}
	w.key = filepath.Join(w.root, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t@test", "-f", w.key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(w.key + ".pub")
	f := strings.Fields(string(data))
	w.pub = "t@test namespaces=\"git\" " + f[0] + " " + f[1] + "\n"
	return w
}

func (w *repoWorld) git(dir string, args ...string) string {
	w.t.Helper()
	cfg := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@test", "-c", "gpg.format=ssh",
		"-c", "user.signingKey=" + w.key + ".pub", "-c", "commit.gpgsign=true"}
	out, err := exec.Command("git", append(cfg, args...)...).CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// origin makes a bare repository called name, with a work repository beside
// it; main holds a commit (with fleet/allowed_signers when withSigners) and
// host/nas another. It returns the address and the work folder.
func (w *repoWorld) origin(name string, withSigners bool) (address, work string) {
	w.t.Helper()
	bare := filepath.Join(w.root, name, name+".git")
	work = filepath.Join(w.root, name, "work")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		w.t.Fatalf("%v\n%s", err, out)
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", work).CombinedOutput(); err != nil {
		w.t.Fatalf("%v\n%s", err, out)
	}
	address = "file://" + bare
	w.git(work, "remote", "add", "origin", address)
	if withSigners {
		_ = os.MkdirAll(filepath.Join(work, "fleet"), 0o755)
		_ = os.WriteFile(filepath.Join(work, "fleet", "allowed_signers"), []byte(w.pub), 0o644)
		w.git(work, "add", "fleet/allowed_signers")
	}
	w.git(work, "commit", "-q", "--allow-empty", "-m", "main")
	w.git(work, "switch", "-q", "-c", "host/nas")
	w.git(work, "commit", "-q", "--allow-empty", "-m", "nas")
	w.git(work, "switch", "-q", "main")
	w.git(work, "push", "-q", "origin", "main", "host/nas")
	return address, work
}

func (w *repoWorld) ctx(opts engine.Options) *engine.Ctx {
	w.out.Reset()
	return &engine.Ctx{Context: context.Background(), UI: ui.NewWith(w.out, nil), Runner: engine.Exec{}, Options: opts,
		Values: map[string]string{}, Stdin: strings.NewReader(""), LookPath: exec.LookPath}
}

func (w *repoWorld) add(address string, extra map[string]string) error {
	flags := map[string]string{"url": address, "signing-key": w.key}
	for k, v := range extra {
		flags[k] = v
	}
	return engine.RunWith(w.ctx(engine.Options{Yes: true}), RepoAdd, flags)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRepoAddClonesTrustsAndNamesARepositoryWithItsOwnSigners(t *testing.T) {
	w := newRepoWorld(t)
	address, work := w.origin("inventory", true)
	if err := w.add(address, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	id, _ := fleet.RepoID(address)
	clone, _ := fleet.ClonePath(address)
	if names, _ := fleet.ReadNames(); names["inventory"] != id {
		t.Errorf("names = %v", names)
	}
	main := gitOut(t, work, "rev-parse", "main")
	nas := gitOut(t, work, "rev-parse", "host/nas")
	for branch, want := range map[string]string{"main": main, "host/nas": nas} {
		if got, _ := fleet.ReadTrusted(address, branch); got != want {
			t.Errorf("trusted %s = %q, want %q", branch, got, want)
		}
	}
	copyPath, _ := fleet.SignersCopy(address)
	if got := gitOut(t, clone, "config", "gpg.ssh.allowedSignersFile"); got != copyPath {
		t.Errorf("gpg.ssh.allowedSignersFile = %q, want %q", got, copyPath)
	}
	if data, _ := os.ReadFile(copyPath); string(data) != w.pub {
		t.Errorf("the copy is %q", data)
	}
	for k, v := range map[string]string{"gpg.format": "ssh", "user.signingKey": w.key + ".pub", "commit.gpgSign": "true", "tag.gpgSign": "true"} {
		if got := gitOut(t, clone, "config", k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if got := gitOut(t, clone, "rev-parse", "HEAD"); got != main {
		t.Errorf("the clone is at %s, want %s", got, main)
	}
	if err := exec.Command("git", "-C", clone, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		t.Error("the clone is on a branch")
	}
	if !strings.Contains(w.out.String(), "fleet inventory publish --flake inventory") {
		t.Errorf("no hint:\n%s", w.out)
	}
	// Again: nothing to do, and no complaint.
	if err := w.add(address, nil); err != nil {
		t.Fatalf("again: %v\n%s", err, w.out)
	}
	if !strings.Contains(w.out.String(), "cloned already") {
		t.Errorf("again:\n%s", w.out)
	}
	// A clone left on a branch, as git clone leaves it when the checkout
	// after it failed, is checked out the next time.
	w.git(clone, "checkout", "-q", "main")
	if err := w.add(address, nil); err != nil {
		t.Fatalf("after a failed checkout: %v\n%s", err, w.out)
	}
	if !strings.Contains(w.out.String(), "but not checked out") {
		t.Errorf("after a failed checkout:\n%s", w.out)
	}
	if err := exec.Command("git", "-C", clone, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		t.Error("the clone is still on a branch")
	}
	// The name belongs to the one repository.
	other, _ := w.origin("other", true)
	err := w.add(other, map[string]string{"name": "inventory"})
	if err == nil || !strings.Contains(w.out.String(), "inventory already names "+id) {
		t.Errorf("a taken name: %v\n%s", err, w.out)
	}
}

func TestRepoAddOfARepositoryWithoutSignersTakesAnotherNamesKeys(t *testing.T) {
	w := newRepoWorld(t)
	inv, _ := w.origin("inventory", true)
	sec, secWork := w.origin("secrets", false)
	// Its keys are another repository's: without a name for it, no.
	err := w.add(sec, nil)
	if err == nil || !strings.Contains(w.out.String(), "--signers-from") {
		t.Fatalf("no signers-from: %v\n%s", err, w.out)
	}
	if err := w.add(sec, map[string]string{"signers-from": "inventory"}); err == nil || !strings.Contains(w.out.String(), `"inventory" is not the name of a repository`) {
		t.Fatalf("unknown signers-from: %v\n%s", err, w.out)
	}
	if err := w.add(inv, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	// Without a terminal to choose on, it names the repositories to choose from.
	w.out.Reset()
	if err := w.add(sec, nil); err == nil || !strings.Contains(w.out.String(), "give --signers-from NAME, one of: inventory.") {
		t.Fatalf("no choices named: %v\n%s", err, w.out)
	}
	// The failed tries left a clone and no name; adding it right now finishes.
	if names, _ := fleet.ReadNames(); names["secrets"] != "" {
		t.Fatalf("named before it worked: %v", names)
	}
	if err := w.add(sec, map[string]string{"signers-from": "inventory"}); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	id, _ := fleet.RepoID(sec)
	if names, _ := fleet.ReadNames(); names["secrets"] != id {
		t.Errorf("names = %v", names)
	}
	invCopy, _ := fleet.SignersCopy(inv)
	clone, _ := fleet.ClonePath(sec)
	if got := gitOut(t, clone, "config", "gpg.ssh.allowedSignersFile"); got != invCopy {
		t.Errorf("gpg.ssh.allowedSignersFile = %q, want the inventory's copy %q", got, invCopy)
	}
	for _, branch := range []string{"main", "host/nas"} {
		if got, _ := fleet.ReadTrusted(sec, branch); got == "" {
			t.Errorf("no record for %s", branch)
		}
	}
	if own, _ := fleet.SignersCopy(sec); fileExists(own) {
		t.Error("a copy of its own was made")
	}

	// A name resolves to the clone, and the clone follows origin.
	x := w.ctx(engine.Options{Yes: true})
	got, err := fleet.ResolveName(x, "repo", "secrets")
	if err != nil || got != clone || x.Values["repo_name"] != "secrets" {
		t.Fatalf("ResolveName = %q, %v, %v", got, err, x.Values)
	}
	w.git(secWork, "commit", "-q", "--allow-empty", "-m", "two")
	w.git(secWork, "push", "-q", "origin", "main")
	want := gitOut(t, secWork, "rev-parse", "main")
	if err := fleet.SyncClone(x, got); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if h := gitOut(t, clone, "rev-parse", "HEAD"); h != want {
		t.Errorf("the clone is at %s, want %s", h, want)
	}
	if rec, _ := fleet.ReadTrusted(sec, "main"); rec != want {
		t.Errorf("record = %s", rec)
	}
}

func TestRepoAddDryRunChangesNothing(t *testing.T) {
	w := newRepoWorld(t)
	address, _ := w.origin("inventory", true)
	err := engine.RunWith(w.ctx(engine.Options{DryRun: true}), RepoAdd, map[string]string{"url": address, "signing-key": w.key})
	if err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	clone, _ := fleet.ClonePath(address)
	if fileExists(clone) {
		t.Error("cloned in a dry run")
	}
	if names, _ := fleet.ReadNames(); len(names) != 0 {
		t.Errorf("named in a dry run: %v", names)
	}
}

func TestACommandGivenANameFirstBringsTheClonesUpToDate(t *testing.T) {
	w := newRepoWorld(t)
	address, work := w.origin("inventory", true)
	if err := w.add(address, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if TrustSigners.Steps[0].Title != cloneSync.Title {
		t.Fatalf("the first step is %q", TrustSigners.Steps[0].Title)
	}
	w.git(work, "commit", "-q", "--allow-empty", "-m", "two")
	w.git(work, "push", "-q", "origin", "main")
	want := gitOut(t, work, "rev-parse", "main")
	x := w.ctx(engine.Options{Yes: true})
	if err := engine.RunWith(x, TrustSigners, map[string]string{"flake": "inventory"}); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	clone, _ := fleet.ClonePath(address)
	if x.Value("flake") != clone || x.Value("flake_name") != "inventory" {
		t.Errorf("values: %v", x.Values)
	}
	if h := gitOut(t, clone, "rev-parse", "HEAD"); h != want {
		t.Errorf("the clone is at %s, want %s", h, want)
	}
	if rec, _ := fleet.ReadTrusted(address, "main"); rec != want {
		t.Errorf("record = %s", rec)
	}
	// A path is used as it is, and the clone step is skipped.
	x = w.ctx(engine.Options{Yes: true})
	if err := engine.RunWith(x, TrustSigners, map[string]string{"flake": clone}); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if !strings.Contains(w.out.String(), "skipped: a checkout or flake given by path") {
		t.Errorf("by path:\n%s", w.out)
	}
	// A word that is no name is refused before anything runs.
	err := engine.RunWith(w.ctx(engine.Options{Yes: true}), TrustSigners, map[string]string{"flake": "nothing"})
	if err == nil || !strings.Contains(err.Error(), "neither a path nor the name of a repository") {
		t.Errorf("unknown name: %v", err)
	}
}

func TestRepoListShowsNamesRecordsAndRepositoriesWithoutAName(t *testing.T) {
	w := newRepoWorld(t)
	address, _ := w.origin("inventory", true)
	if err := w.add(address, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	// Records for a repository that has no name.
	if err := fleet.WriteTrusted("git@github.com:example-org/old.git", "main", strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	if err := engine.RunWith(w.ctx(engine.Options{Yes: true}), RepoList, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	clone, _ := fleet.ClonePath(address)
	out := w.out.String()
	for _, want := range []string{"inventory", address, clone, "main ", "host/nas ", "Without a name", "github.com/example-org/old"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
}
