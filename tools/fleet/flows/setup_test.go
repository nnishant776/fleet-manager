package flows

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

func newFakeCtx(respond func(engine.Spec) (engine.Result, error)) (*engine.Ctx, *engine.Fake, *bytes.Buffer) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: func(s engine.Spec) (engine.Result, error) {
		res, err := respond(s)
		// A checkout has an origin unless a test says otherwise.
		if err == nil && res.Stdout == "" && s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "remote get-url origin") {
			res.Stdout = "git@github.com:example-org/inventory.git\n"
		}
		return res, err
	}}
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
			_ = os.WriteFile(s.Args[i+1]+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx fleet\n"), 0o644)
		}
	}
}

func TestSetupOnAFreshMachine(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.MkdirAll(filepath.Join(repo, ".githooks"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".githooks", "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "ssh-keygen":
			keygen(s)
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
		"add fleet/allowed_signers", "trust me@test", "age-keygen -y"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
	signers, _ := os.ReadFile(filepath.Join(config, "fleet", "allowed_signers"))
	if string(signers) != "me@test namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx\n" {
		t.Errorf("allowed_signers is %q", signers)
	}
	// Your copy of the keys, outside the checkout, which git checks against.
	copyPath, _ := fleet.SignersCopy("git@github.com:example-org/inventory.git")
	if got, _ := os.ReadFile(copyPath); string(got) != string(signers) {
		t.Errorf("your copy is %q, want the keys you approved, %q", got, signers)
	}
	if !strings.Contains(cmds, "config gpg.ssh.allowedSignersFile "+copyPath) {
		t.Errorf("git does not check against your copy:\n%s", cmds)
	}
	if strings.Contains(cmds, "config gpg.ssh.allowedSignersFile "+filepath.Join(config, "fleet", "allowed_signers")) {
		t.Error("git checks against the checkout's own file")
	}
	// The hook is shown, then copied into .git/hooks: git never runs the
	// repository's own file.
	if hook, _ := os.ReadFile(filepath.Join(repo, ".git", "hooks", "pre-commit")); string(hook) != "#!/bin/sh\nexit 0\n" {
		t.Errorf("the installed hook is %q", hook)
	}
	if !strings.Contains(out.String(), "exit 0") {
		t.Errorf("the hook was not shown before it was installed:\n%s", out)
	}
	if strings.Contains(cmds, "core.hooksPath .githooks") {
		t.Error("pointed git at the repository's own hook")
	}
	// The recovery recipient is cached, for the secrets commands to encrypt to;
	// the secrets repository gets no commit and no push.
	if got, _ := os.ReadFile(filepath.Join(state, "fleet", "recovery.pub")); string(got) != "age1freshrecipient\n" {
		t.Errorf("the recovery cache holds %q", got)
	}
	for _, unwanted := range []string{"fleet.yaml", " push"} {
		if strings.Contains(cmds, unwanted) {
			t.Errorf("ran %q:\n%s", unwanted, cmds)
		}
	}
	if strings.Contains(cmds, "-C "+repo+" commit") {
		t.Errorf("committed in the secrets repository:\n%s", cmds)
	}
}

func TestSetupSkipsWhatIsDone(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	_ = os.MkdirAll(filepath.Join(state, "fleet"), 0o755)
	_ = os.WriteFile(filepath.Join(state, "fleet", "recovery.pub"), []byte("age1donerecipient\n"), 0o644)
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "recovery.age"), []byte("encrypted"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing"), []byte("private"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "signing.pub"), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB4cTxXwY1G40AfKlCx6sZEOmkWK4TNdbOHgtpKe+Qc7 fleet\n"), 0o644)
	_ = os.WriteFile(filepath.Join(config, "fleet", "allowed_signers"), []byte("me@test namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB4cTxXwY1G40AfKlCx6sZEOmkWK4TNdbOHgtpKe+Qc7\n"), 0o644)
	for _, d := range []string{".githooks", ".git/hooks"} {
		_ = os.MkdirAll(filepath.Join(repo, d), 0o755)
		_ = os.WriteFile(filepath.Join(repo, d, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	}
	want := map[string]string{
		"gpg.format": "ssh", "user.signingKey": filepath.Join(dir, "signing") + ".pub", "commit.gpgSign": "true",
		"tag.gpgSign": "true", "gpg.ssh.allowedSignersFile": filepath.Join(config, "fleet", "allowed_signers"),
	}
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
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
	for _, unwanted := range []string{"age -p", "age -d", "ssh-keygen", "commit -q", "push", "config gpg.format"} {
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
		"host": "pi", "flake": "/no/such/flake", "old-key": oldKey, "new-key": newKey, "from": "192.168.0.0/24",
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
	if !strings.Contains(out.String(), `from=\"192.168.0.0/24\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx fleet`) {
		t.Errorf("the line to add is not shown with its from= restriction:\n%s", out)
	}
	if !strings.Contains(out.String(), "In hosts/pi/default.nix, under conf.fleet.support.sshKeys") {
		t.Errorf("the instructions do not name the host:\n%s", out)
	}
	if !strings.Contains(out.String(), "✓ the old key is refused") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupDryRunOnAFreshMachineChangesNothing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	config, repo := filepath.Join(dir, "config"), filepath.Join(dir, "secrets")
	_ = os.MkdirAll(filepath.Join(config, "fleet"), 0o755)
	_ = os.MkdirAll(repo, 0o755)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
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
		if !(s.Name == "git" && (strings.Contains(s.String(), "--get") || strings.Contains(s.String(), "remote get-url"))) {
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

func TestSetupInstallsTheHookWhereAnOlderSetupPointedGitAtTheRepositorysOwn(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.hooksPath", ".githooks"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tracked := filepath.Join(repo, ".githooks", "pre-commit")
	hook := []byte("#!/bin/sh\nexit 0\n")
	_ = os.MkdirAll(filepath.Dir(tracked), 0o755)
	_ = os.WriteFile(tracked, hook, 0o755)
	x, _, out := newFakeCtx(nil)
	x.Runner = engine.Exec{}
	x.Values["repo"] = repo
	if need, _ := hookNotInstalled(x); !need {
		t.Fatal("a hook that git is pointed at the repository's own file counts as installed")
	}
	if err := installHook(x); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	installed := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if got, _ := os.ReadFile(installed); !bytes.Equal(got, hook) {
		t.Errorf("the installed hook is %q", got)
	}
	if fi, err := os.Stat(installed); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed hook is not executable: %v %v", fi, err)
	}
	if got, _ := os.ReadFile(tracked); !bytes.Equal(got, hook) {
		t.Errorf("the repository's hook changed to %q", got)
	}
	if v, err := exec.Command("git", "-C", repo, "config", "--local", "--get", "core.hooksPath").Output(); err == nil {
		t.Errorf("core.hooksPath is still %q", v)
	}
}

// With conf.fleet.support.keysFromSecrets the rotation changes support's keys
// in the secrets, with user-keys, and shows no configuration lines.
func TestSupportKeyRotationOnAHostWithKeysFromSecrets(t *testing.T) {
	dir := t.TempDir()
	oldKey, newKey := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	oldLine := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLDOLD old"
	if err := os.WriteFile(oldKey+".pub", []byte(oldLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var written []string // what each user-keys call was given
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.support.keysFromSecrets"):
			return engine.Result{Stdout: "true\n"}, nil
		case s.Name == "fleet":
			for i, a := range s.Args {
				if a == "--authorized-keys" {
					b, _ := os.ReadFile(s.Args[i+1])
					written = append(written, string(b))
				}
			}
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
		"host": "pi", "flake": "/flake", "old-key": oldKey, "new-key": newKey, "from": "192.168.0.0/24",
		"repo": "/secrets", "recovery": "/rec.age",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	newLine := `from="192.168.0.0/24" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx fleet`
	if len(written) != 2 || written[0] != oldLine+"\n"+newLine+"\n" || written[1] != newLine+"\n" {
		t.Errorf("user-keys was given %q", written)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, want := range []string{
		"fleet secrets user-keys support pi --authorized-keys",
		"-i " + oldKey + " support@pi 'sudo -n systemctl start config-auto-update'",
		"-i " + newKey + " support@pi 'sudo -n id -u'",
		"fleet secrets user-keys support pi --authorized-keys",
		"-i " + newKey + " support@pi 'sudo -n systemctl start config-auto-update'",
		"-i " + oldKey + " support@pi true",
	} {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i + len(want)
	}
	if strings.Contains(out.String(), "In hosts/pi/default.nix") {
		t.Errorf("configuration lines were shown:\n%s", out)
	}
}
