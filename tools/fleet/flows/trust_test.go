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
	keyMine  = "AAAAC3NzaC1lZDI1NTE5AAAAIB4cTxXwY1G40AfKlCx6sZEOmkWK4TNdbOHgtpKe+Qc7"
	keyOther = "AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx"
)

func signersLine(label, key string) string {
	return label + " namespaces=\"git\" ssh-ed25519 " + key + "\n"
}

// trustCheckout is a checkout of git@github.com:example-org/inventory.git
// whose fleet/allowed_signers is signers, with git signing as keyMine; and a
// state folder of its own.
func trustCheckout(t *testing.T, signers string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "fleet"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "fleet", "allowed_signers"), []byte(signers), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "signing.pub"), []byte("ssh-ed25519 "+keyMine+" me\n"), 0o644)
	return dir
}

// trustRepo answers like git in that checkout; signedByTrusted says whether
// the commit that changed the signers file verifies against the copy.
func trustRepo(dir string, signedByTrusted bool) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "git" && strings.Contains(args, "remote get-url origin"):
			return engine.Result{Stdout: "git@github.com:example-org/inventory.git\n"}, nil
		case s.Name == "git" && strings.Contains(args, "config user.signingKey"):
			return engine.Result{Stdout: filepath.Join(dir, "signing.pub") + "\n"}, nil
		case s.Name == "git" && strings.Contains(args, "log -1 --format=%H -- fleet/allowed_signers"):
			return engine.Result{Stdout: pinnedINV + "\n"}, nil
		case s.Name == "git" && strings.Contains(args, "verify-commit "+pinnedINV):
			if !signedByTrusted {
				return engine.Result{Code: 1}, errors.New("exited with code 1")
			}
		}
		return engine.Result{}, nil
	}
}

func runTrust(t *testing.T, dir string, respond func(engine.Spec) (engine.Result, error)) (error, string) {
	t.Helper()
	x, _, out := newFakeCtx(respond)
	err := engine.RunWith(x, TrustSigners, map[string]string{"flake": dir})
	return err, out.String()
}

func copyOf(t *testing.T) string {
	t.Helper()
	p, _ := fleet.SignersCopy("git@github.com:example-org/inventory.git")
	data, _ := os.ReadFile(p)
	return string(data)
}

func TestTrustSignersTakesTheFirstCopyOnceApproved(t *testing.T) {
	signers := signersLine("me@test", keyMine) + signersLine("other@test", keyOther)
	dir := trustCheckout(t, signers)
	err, out := runTrust(t, dir, trustRepo(dir, true))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if copyOf(t) != signers {
		t.Errorf("the copy is %q", copyOf(t))
	}
	if !strings.Contains(out, "me@test ssh-ed25519 SHA256:8MIxN7gegMpbo32xsB9SnhR8FmxFSqyhNhTrsj8D5I4  (yours)") {
		t.Errorf("your key is not marked:\n%s", out)
	}
}

func TestTrustSignersTakesAChangeSignedByATrustedKey(t *testing.T) {
	dir := trustCheckout(t, signersLine("me@test", keyMine))
	if err, out := runTrust(t, dir, trustRepo(dir, true)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	changed := signersLine("me@test", keyMine) + signersLine("other@test", keyOther)
	_ = os.WriteFile(filepath.Join(dir, "fleet", "allowed_signers"), []byte(changed), 0o644)
	err, out := runTrust(t, dir, trustRepo(dir, true))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "+ other@test ssh-ed25519") {
		t.Errorf("the added key is not shown:\n%s", out)
	}
	if copyOf(t) != changed {
		t.Errorf("the copy was not updated: %q", copyOf(t))
	}
}

// A pushed commit that adds the key it is signed with cannot update the copy.
func TestTrustSignersRefusesAChangeNoTrustedKeySigned(t *testing.T) {
	first := signersLine("me@test", keyMine)
	dir := trustCheckout(t, first)
	if err, out := runTrust(t, dir, trustRepo(dir, true)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(dir, "fleet", "allowed_signers"), []byte(first+signersLine("attacker", keyOther)), 0o644)
	err, out := runTrust(t, dir, trustRepo(dir, false))
	if err == nil || !strings.Contains(out, "is not signed by a key you trust, so your copy stays as it is") {
		t.Fatalf("took a change no trusted key signed: %v\n%s", err, out)
	}
	if copyOf(t) != first {
		t.Errorf("the copy changed: %q", copyOf(t))
	}
}

// trustCopies gives the test a state folder of its own, with a copy of the
// keys for each repository address.
func trustCopies(t *testing.T, addresses ...string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, a := range addresses {
		p, err := fleet.SignersCopy(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := fleet.WriteSignersCopy(p, []byte(signersLine("me@test", keyMine))); err != nil {
			t.Fatal(err)
		}
	}
}

// flat collapses whitespace: notes are wrapped to the terminal.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func runSignersCheck(t *testing.T, dir string) string {
	t.Helper()
	x, _, out := newFakeCtx(trustRepo(dir, true))
	f := &engine.Flow{Title: "t", Inputs: []engine.Input{fleet.FlakeInput}, Steps: []engine.Step{signersCheck}}
	if err := engine.RunWith(x, f, map[string]string{"flake": dir}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return flat(out.String())
}

// Commands that read a checkout warn when its keys differ from yours, and
// say how to update the repository's trust.
func TestCommandsWarnWhenTheCheckoutsKeysDiffer(t *testing.T) {
	dir := trustCheckout(t, signersLine("me@test", keyMine))
	if out := runSignersCheck(t, dir); !strings.Contains(out, "You trust no keys for github.com/example-org/inventory yet") ||
		!strings.Contains(out, "fleet trust signers --flake "+dir) {
		t.Errorf("no note without a copy:\n%s", out)
	}
	if err, out := runTrust(t, dir, trustRepo(dir, true)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out := runSignersCheck(t, dir); !strings.Contains(out, "the checkout lists the keys you trust") {
		t.Errorf("no ✓ for the same keys:\n%s", out)
	}
	_ = os.WriteFile(filepath.Join(dir, "fleet", "allowed_signers"), []byte(signersLine("me@test", keyMine)+signersLine("attacker", keyOther)), 0o644)
	out := runSignersCheck(t, dir)
	for _, want := range []string{"differ from the ones you trust", "+ attacker ssh-ed25519",
		"update the repository's trust: fleet trust signers --flake " + dir} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning lacks %q:\n%s", want, out)
		}
	}
}

// Every command that takes --flake starts with the tool's clone brought up to
// date (when it was given a name), then that comparison; one that takes only
// --repo has the clone step first.
func TestEveryCommandWithAFlakeComparesKeysFirst(t *testing.T) {
	for _, f := range All() {
		name := strings.Join(f.Path, " ")
		if !hasInput(f, "flake") && !hasInput(f, "repo") {
			continue
		}
		if f.Steps[0].Title != cloneSync.Title {
			t.Errorf("fleet %s starts with %q", name, f.Steps[0].Title)
		}
		if f == TrustSigners || !hasInput(f, "flake") {
			continue
		}
		if f.Steps[1].Title != signersCheck.Title {
			t.Errorf("fleet %s then has %q", name, f.Steps[1].Title)
		}
	}
}

func TestTrustSignersPointsTheCheckoutsGitAtYourCopy(t *testing.T) {
	dir := trustCheckout(t, signersLine("me@test", keyMine))
	x, fake, out := newFakeCtx(trustRepo(dir, true))
	if err := engine.RunWith(x, TrustSigners, map[string]string{"flake": dir}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	copyPath, _ := fleet.SignersCopy("git@github.com:example-org/inventory.git")
	want := "git -C " + dir + " config gpg.ssh.allowedSignersFile " + copyPath
	if cmds := strings.Join(fake.Commands(), "\n"); !strings.Contains(cmds, want) {
		t.Errorf("did not run %q:\n%s", want, cmds)
	}
	for _, c := range fake.Commands() {
		for _, other := range []string{"gpg.format ", "user.signingkey ", "commit.gpgsign "} {
			if strings.Contains(strings.ToLower(c), "config "+other) {
				t.Errorf("configured signing: %s", c)
			}
		}
	}
}

func TestTrustSignersLeavesGitAloneWhenItAlreadyChecksYourCopy(t *testing.T) {
	signers := signersLine("me@test", keyMine)
	dir := trustCheckout(t, signers)
	copyPath, _ := fleet.SignersCopy("git@github.com:example-org/inventory.git")
	_ = fleet.WriteSignersCopy(copyPath, []byte(signers))
	respond := trustRepo(dir, true)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "config --local --get gpg.ssh.allowedSignersFile") {
			return engine.Result{Stdout: copyPath + "\n"}, nil
		}
		return respond(s)
	})
	if err := engine.RunWith(x, TrustSigners, map[string]string{"flake": dir}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, " config gpg.") {
			t.Errorf("changed git's settings though it checks your copy: %s", c)
		}
	}
}
