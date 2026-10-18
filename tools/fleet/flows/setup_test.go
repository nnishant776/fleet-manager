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

func newFakeCtx(respond func(engine.Spec) (engine.Result, error)) (*engine.Ctx, *engine.Fake, *bytes.Buffer) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: respond}
	return &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Options: engine.Options{Yes: true}, Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}, fake, &out
}

// keygen makes the fake ssh-keygen leave a public key where -f says.
func keygen(s engine.Spec) {
	for i, a := range s.Args {
		if a == "-f" && i+1 < len(s.Args) {
			_ = os.WriteFile(s.Args[i+1]+".pub", []byte("ssh-ed25519 AAAAC3NzaFRESH fleet\n"), 0o644)
		}
	}
}

func TestSetupOnAFreshMachine(t *testing.T) {
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "ssh-keygen":
			keygen(s)
		case s.Name == "yq" && s.Args[0] == ".maintainer":
			return engine.Result{Stdout: "null\n"}, nil
		case s.Name == "sh" && strings.Contains(s.Args[1], "age-keygen -y"):
			return engine.Result{Stdout: "age1freshrecipient\n"}, nil
		}
		return engine.Result{}, nil
	})
	err := engine.RunWith(x, Setup, map[string]string{
		"config": config, "repo": repo, "recovery": filepath.Join(dir, "fleet", "recovery.age"),
		"signing-key": filepath.Join(dir, "signing"), "label": "me@test",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, want := range []string{"age-keygen | age -p -o", "ssh-keygen -t ed25519", "config gpg.format ssh",
		"config core.hooksPath .githooks", "add fleet/allowed_signers", "trust me@test",
		"yq -i '.maintainer = strenv(r)' fleet.yaml", "set maintainer recipient", "git push"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
	signers, _ := os.ReadFile(filepath.Join(config, "fleet", "allowed_signers"))
	if string(signers) != "me@test namespaces=\"git\" ssh-ed25519 AAAAC3NzaFRESH\n" {
		t.Errorf("allowed_signers is %q", signers)
	}
	for _, r := range fake.Ran {
		if r.Name == "yq" && strings.Contains(strings.Join(r.Args, " "), ".maintainer =") {
			if len(r.Env) != 1 || r.Env[0] != "r=age1freshrecipient" {
				t.Errorf("the maintainer was set to %q", r.Env)
			}
		}
	}
}

func TestSetupSkipsWhatIsDone(t *testing.T) {
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "recovery.age"), []byte("encrypted"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing"), []byte("private"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing.pub"), []byte("ssh-ed25519 AAAAC3NzaDONE fleet\n"), 0o644)
	_ = os.WriteFile(filepath.Join(config, "fleet", "allowed_signers"), []byte("me@test namespaces=\"git\" ssh-ed25519 AAAAC3NzaDONE\n"), 0o644)
	want := map[string]string{
		"gpg.format": "ssh", "user.signingKey": filepath.Join(dir, "signing") + ".pub", "commit.gpgSign": "true",
		"tag.gpgSign": "true", "gpg.ssh.allowedSignersFile": filepath.Join(config, "fleet", "allowed_signers"),
		"core.hooksPath": ".githooks",
	}
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "yq" && s.Args[0] == ".maintainer":
			return engine.Result{Stdout: "age1donerecipient\n"}, nil
		case s.Name == "git" && len(s.Args) > 4 && s.Args[2] == "config" && s.Args[4] == "--get":
			if s.Args[1] == config && s.Args[5] == "gpg.ssh.allowedSignersFile" {
				// Relative, as git config writes it when run in the checkout.
				return engine.Result{Stdout: "fleet/allowed_signers\n"}, nil
			}
			return engine.Result{Stdout: want[s.Args[5]] + "\n"}, nil
		}
		return engine.Result{}, nil
	})
	err := engine.RunWith(x, Setup, map[string]string{
		"config": config, "repo": repo, "recovery": filepath.Join(dir, "recovery.age"),
		"signing-key": filepath.Join(dir, "signing"), "label": "me@test",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, unwanted := range []string{"age -p", "ssh-keygen", "commit -q", "push", "config gpg.format"} {
		for _, c := range fake.Commands() {
			if strings.Contains(c, unwanted) {
				t.Errorf("ran %q on a machine that is set up: %s", unwanted, c)
			}
		}
	}
}

func TestSupportKeyRotationInOrder(t *testing.T) {
	dir := t.TempDir()
	oldKey, newKey := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "ssh-keygen":
			keygen(s)
		case s.Name == "ssh" && strings.Contains(args, "-i "+newKey) && strings.HasSuffix(args, "sudo -n id -u"):
			return engine.Result{Stdout: "0\n"}, nil
		case s.Name == "ssh" && strings.Contains(args, "-i "+oldKey) && strings.HasSuffix(args, " true"):
			return engine.Result{Code: 255}, errors.New("exited with code 255")
		}
		return engine.Result{}, nil
	})
	err := engine.RunWith(x, SupportKeyRotate, map[string]string{
		"host": "pi", "flake": "/etc/nixos", "old-key": oldKey, "new-key": newKey, "from": "192.168.0.0/24",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	order := []string{
		"ssh-keygen -t ed25519-sk -O resident -O verify-required",
		"-i " + oldKey + " support@pi 'sudo -n systemctl start config-auto-update'",
		"-i " + newKey + " support@pi 'sudo -n id -u'",
		"-i " + newKey + " support@pi 'sudo -n systemctl start config-auto-update'",
		"-i " + oldKey + " support@pi true",
	}
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, want := range order {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
	for _, s := range fake.Ran {
		if s.Name == "ssh" && !strings.Contains(s.String(), "IdentityAgent=none") {
			t.Errorf("an ssh offered agent keys: %s", s)
		}
	}
	if !strings.Contains(out.String(), `from=\"192.168.0.0/24\" ssh-ed25519 AAAAC3NzaFRESH fleet`) {
		t.Errorf("the line to add is not shown with its from= restriction:\n%s", out)
	}
	if !strings.Contains(out.String(), "✓ the old key is refused") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupDryRunOnAFreshMachineChangesNothing(t *testing.T) {
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "yq" {
			return engine.Result{Stdout: "null\n"}, nil
		}
		return engine.Result{}, nil
	})
	x.Options = engine.Options{DryRun: true}
	err := engine.RunWith(x, Setup, map[string]string{
		"config": config, "repo": repo, "recovery": filepath.Join(dir, "fleet", "recovery.age"),
		"signing-key": filepath.Join(dir, "signing"), "label": "me@test",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, s := range fake.Ran {
		if !(s.Name == "yq" && s.Args[0] == ".maintainer") && !(s.Name == "git" && strings.Contains(s.String(), "--get")) {
			t.Errorf("a dry run ran %s", s)
		}
	}
	if signers, _ := os.ReadFile(filepath.Join(config, "fleet", "allowed_signers")); len(signers) != 0 {
		t.Errorf("a dry run wrote allowed_signers: %q", signers)
	}
	if fileExists(filepath.Join(dir, "fleet")) {
		t.Error("a dry run made the recovery directory")
	}
}
