package flows

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

var errFailed = errors.New("exited with code 1")

// after is what ran after the first command containing fragment.
func after(cmds []string, fragment string) []string {
	for i, c := range cmds {
		if strings.Contains(c, fragment) {
			return cmds[i+1:]
		}
	}
	return nil
}

func TestAdmittingAHostPutsBackWhenTheCommitFails(t *testing.T) {
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "fleet.yaml"), []byte("maintainer: age1maint\nhosts: {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".sops.yaml"), []byte("creation_rules: []\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "hosts"), 0o755)
	from := filepath.Join(t.TempDir(), "pi.enroll")
	_ = os.WriteFile(from, []byte(enrollDoc), 0o644)
	answers := map[string]string{
		".hosts | has(strenv(h))": "false", ".version": "1", ".host": "pi",
		".age_recipient": "age1testrecipient", ".secrets_deploy_pubkey // .deploy_pubkey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDdBK90 fleet-deploy@pi",
		".maintainer": "age1maint",
	}
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "yq" && answers[s.Args[0]] != "":
			return engine.Result{Stdout: answers[s.Args[0]] + "\n"}, nil
		case s.Name == "sops":
			return engine.Result{Stdout: "sops: encrypted\n"}, nil
		case s.Name == "git" && s.Args[0] == "commit":
			return engine.Result{Code: 1, Stderr: "error: no signing key"}, errFailed
		}
		return engine.Result{}, nil // git cat-file and git diff: committed and unchanged
	})
	err := engine.RunWith(x, SecretsHostEnroll, map[string]string{"repo": repo, "recovery": "", "host": "pi", "from": from})
	if err == nil || !strings.Contains(err.Error(), "Commit") {
		t.Fatalf("got %v\n%s", err, out)
	}
	want := []string{
		"git -C " + repo + " reset -q -- fleet.yaml .sops.yaml hosts/pi.yaml",
		"rm -f -- " + filepath.Join(repo, "hosts/pi.yaml"),
		"git -C " + repo + " restore --source=HEAD --staged --worktree -- .sops.yaml",
		"git -C " + repo + " restore --source=HEAD --staged --worktree -- fleet.yaml",
	}
	if got := after(fake.Commands(), "commit -q"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("put back with:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(out.String(), "[stop] Put back what this run changed") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupPutsBackAllowedSignersFromItsCopy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "recovery.age"), []byte("encrypted"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing"), []byte("private"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing.pub"), []byte("ssh-ed25519 AAAAC3NzaNEW fleet\n"), 0o644)
	signers := filepath.Join(config, "fleet", "allowed_signers")
	before := "someone namespaces=\"git\" ssh-ed25519 AAAAC3NzaOTHER\n"
	_ = os.WriteFile(signers, []byte(before), 0o644)
	x, _, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "yq":
			return engine.Result{Stdout: "age1donerecipient\n"}, nil
		case strings.Contains(args, "cat-file"):
			return engine.Result{Code: 1}, errFailed // not committed: only a copy can put it back
		case strings.Contains(args, " commit "):
			return engine.Result{Code: 1}, errFailed
		}
		return engine.Result{}, nil
	})
	err := engine.RunWith(x, Setup, map[string]string{
		"config": config, "repo": repo, "recovery": filepath.Join(dir, "recovery.age"),
		"signing-key": filepath.Join(dir, "signing"), "label": "me@test",
	})
	if err == nil {
		t.Fatalf("the commit failed, but setup went on:\n%s", out)
	}
	if got, _ := os.ReadFile(signers); string(got) != before {
		t.Errorf("allowed_signers is %q, want it as it was, %q\n%s", got, before, out)
	}
	if !strings.Contains(out.String(), "fleet/allowed_signers is as it was, from the copy fleet kept") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAnInterruptedRotationKeepsTheNewKeyAndSaysTheSyncRunsOn(t *testing.T) {
	dir := t.TempDir()
	oldKey, newKey := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "ssh-keygen":
			keygen(s)
		case s.Name == "ssh" && strings.Contains(args, "-i "+oldKey) && strings.Contains(args, "systemctl start"):
			cancel() // Ctrl+C during the sync
			return engine.Result{Code: -1}, errors.New("signal: terminated")
		}
		return engine.Result{}, nil
	})
	x.Context = ctx
	err := engine.RunWith(x, SupportKeyRotate, map[string]string{"host": "pi", "flake": "/no/such/flake", "old-key": oldKey, "new-key": newKey})
	if !errors.Is(err, engine.ErrInterrupted) {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, want := range []string{
		"– kept: the new key, " + newKey,
		"– kept: the sync on pi may still be running there, and finishes by itself; follow it with: ssh support@pi journalctl -fu config-auto-update",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(newKey + ".pub"); err != nil {
		t.Errorf("the new key was removed: %v", err)
	}
	if strings.Contains(strings.Join(fake.Commands(), "\n"), "rm -f") {
		t.Errorf("removed something: %q", fake.Commands())
	}
}

func TestAnInterruptedEnrollmentStillStopsTheAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := enrollHost(false, false, false)
	var out bytes.Buffer
	fake := &engine.Fake{Respond: func(s engine.Spec) (engine.Result, error) {
		if s.Name == "ssh" && strings.Contains(s.Args[len(s.Args)-1], "fleet-secrets-sync init") {
			cancel()
			return engine.Result{Code: -1}, errors.New("signal: terminated")
		}
		return host(s)
	}}
	x := &engine.Ctx{
		Context: ctx, UI: ui.NewWith(&out, nil), Runner: fake, Options: engine.Options{Yes: true},
		Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostEnroll, map[string]string{
		"host": "pi", "flake": "/no/such/flake", "key": "/keys/home", "repo": t.TempDir(), "recovery": "",
		"register-with": "cat >> /forge/authorized_keys", "sync": "false",
	})
	if !errors.Is(err, engine.ErrInterrupted) {
		t.Fatalf("got %v\n%s", err, &out)
	}
	if len(ranMatching(fake, "kill 4242")) != 1 || !strings.Contains(out.String(), "stopped the one-key agent") {
		t.Errorf("the agent was not stopped: %q\n%s", fake.Commands(), &out)
	}
	if strings.Contains(out.String(), "new identity") {
		t.Errorf("an identity that was never made is reported kept:\n%s", &out)
	}
}
