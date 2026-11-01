package fleetmgr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/ui"
)

func adminCtx(t *testing.T, opts engine.Options, respond func(engine.Spec) (engine.Result, error)) (*engine.Ctx, *engine.Fake, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	fake := &engine.Fake{Respond: respond}
	return &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake, Options: opts,
		Values: map[string]string{"host": "laptop1", "address": "laptop1", "admin": "alice", "agent": "/tmp/a.sock",
			"admin_prelude": "", "admin_password": ""},
		Stdin: strings.NewReader(""),
	}, fake, &out
}

const scanned = "10.0.0.5 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHostKeyOfLaptop1\n"

// keyHost answers like ssh's tools for a host behind an alias, at 10.0.0.5,
// whose key known says whether this machine has already.
func keyHost(known bool) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "ssh" && len(s.Args) == 2 && s.Args[0] == "-G":
			return engine.Result{Stdout: "user alice\nhostname 10.0.0.5\nport 22\nuserknownhostsfile ~/.ssh/known_hosts\n"}, nil
		case s.Name == "ssh-keygen" && s.Args[0] == "-F":
			if known {
				return engine.Result{Stdout: "# Host 10.0.0.5 found: line 1\n"}, nil
			}
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		case s.Name == "ssh-keyscan":
			return engine.Result{Stdout: scanned}, nil
		case s.Name == "ssh-keygen" && s.Args[0] == "-l":
			return engine.Result{Stdout: "256 SHA256:abcdef 10.0.0.5 (ED25519)\n"}, nil
		}
		return engine.Result{}, nil
	}
}

func knownHosts(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts"))
	return string(b)
}

func TestAKnownHostKeyIsUsedAsItIs(t *testing.T) {
	x, fake, _ := adminCtx(t, engine.Options{}, keyHost(true))
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".ssh"), 0o700)
	os.WriteFile(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts"), []byte("10.0.0.5 ssh-ed25519 AAAA\n"), 0o600)
	if err := KnowHostKey(x); err != nil {
		t.Fatal(err)
	}
	for _, s := range fake.Ran {
		if s.Name == "ssh-keyscan" {
			t.Fatal("scanned a key this machine knows")
		}
		// Looked up under the name ssh uses, the alias's real address.
		if s.Name == "ssh-keygen" && s.Args[1] != "10.0.0.5" {
			t.Fatalf("looked the key up as %q", s.Args[1])
		}
	}
}

func TestAnUnknownHostKeyIsRecordedWhenItMatchesTheFingerprintGiven(t *testing.T) {
	x, _, out := adminCtx(t, engine.Options{Yes: true}, keyHost(false))
	x.Values["host-key"] = "SHA256:abcdef"
	if err := KnowHostKey(x); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := knownHosts(t); got != "10.0.0.5 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHostKeyOfLaptop1\n" {
		t.Fatalf("known_hosts holds %q", got)
	}
	if !strings.Contains(out.String(), "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub") {
		t.Errorf("did not say how to read the fingerprint on the console:\n%s", out)
	}
}

func TestAHostKeyThatDoesNotMatchIsRefused(t *testing.T) {
	x, _, _ := adminCtx(t, engine.Options{Yes: true}, keyHost(false))
	x.Values["host-key"] = "SHA256:other"
	err := KnowHostKey(x)
	if err == nil || !strings.Contains(err.Error(), "not trusting it") {
		t.Fatalf("got %v", err)
	}
	if knownHosts(t) != "" {
		t.Fatal("recorded a key that did not match")
	}
}

func TestYesAloneDoesNotTrustAHostKey(t *testing.T) {
	x, _, _ := adminCtx(t, engine.Options{Yes: true}, keyHost(false))
	err := KnowHostKey(x)
	if err == nil || !strings.Contains(err.Error(), "--host-key SHA256:abcdef") {
		t.Fatalf("got %v", err)
	}
	if knownHosts(t) != "" {
		t.Fatal("recorded a key nobody compared")
	}
}

func TestAHostKeyBehindAnotherPortIsRecordedUnderThatPort(t *testing.T) {
	x, fake, _ := adminCtx(t, engine.Options{Yes: true}, func(s engine.Spec) (engine.Result, error) {
		if s.Name == "ssh" && s.Args[0] == "-G" {
			return engine.Result{Stdout: "hostname 10.0.0.5\nport 2222\n"}, nil
		}
		return keyHost(false)(s)
	})
	x.Values["host-key"] = "SHA256:abcdef"
	if err := KnowHostKey(x); err != nil {
		t.Fatal(err)
	}
	if got := knownHosts(t); !strings.HasPrefix(got, "[10.0.0.5]:2222 ssh-ed25519 ") {
		t.Fatalf("known_hosts holds %q", got)
	}
	for _, s := range fake.Ran {
		if s.Name == "ssh-keyscan" && !strings.Contains(s.String(), "-p 2222 10.0.0.5") {
			t.Fatalf("scanned with %s", s)
		}
	}
}

// sudoHost answers like a host whose admin's sudo takes a password, right
// when it is the one given.
func sudoHost(needsPassword bool, right string) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		script := s.Args[len(s.Args)-1]
		switch {
		case strings.Contains(script, "echo sudo=key"):
			if needsPassword {
				return engine.Result{Stdout: "sudo=password\n"}, nil
			}
			return engine.Result{Stdout: "sudo=key\n"}, nil
		case strings.Contains(script, "sudo -S -p '' -v"):
			if s.Stdin == right+"\n" {
				return engine.Result{}, nil
			}
			return engine.Result{Code: 1, Stderr: "sudo: 1 incorrect password attempt\n"}, errors.New("exited with code 1")
		}
		return engine.Result{}, nil
	}
}

func TestSudoWithoutAPasswordNeedsNothingMore(t *testing.T) {
	x, fake, _ := adminCtx(t, engine.Options{}, sudoHost(false, ""))
	if err := AdminSudo(x); err != nil {
		t.Fatal(err)
	}
	if len(fake.Ran) != 1 || x.Value("admin_prelude") != "" || x.Value("admin_password") != "" {
		t.Fatalf("did more than probe: %q, prelude %q", fake.Commands(), x.Value("admin_prelude"))
	}
}

func TestSudoWithAPasswordIsCheckedOnceThenHandedToEveryScript(t *testing.T) {
	x, fake, out := adminCtx(t, engine.Options{Yes: true}, sudoHost(true, "s3cret"))
	x.Values["admin-password"] = "s3cret"
	if err := AdminSudo(x); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if x.Value("admin_prelude") == "" {
		t.Fatal("no prelude for the scripts after")
	}
	if _, err := OnAdmin("sudo -n true").Run(x); err != nil {
		t.Fatal(err)
	}
	last := fake.Ran[len(fake.Ran)-1]
	if last.Stdin != "s3cret\n" {
		t.Errorf("the script after got stdin %q", last.Stdin)
	}
	script := last.Args[len(last.Args)-1]
	if !strings.HasPrefix(script, "IFS= read -r FLEETMGR_SUDO_PW") || !strings.HasSuffix(script, "sudo -n true") {
		t.Errorf("the script after is %q", script)
	}
	for _, s := range fake.Ran {
		if strings.Contains(s.String(), "s3cret") {
			t.Errorf("the password is on a command line: %s", s)
		}
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("the password was shown:\n%s", out)
	}
}

func TestAWrongSudoPasswordIsTriedOnceAndNotKept(t *testing.T) {
	x, fake, _ := adminCtx(t, engine.Options{Yes: true}, sudoHost(true, "s3cret"))
	x.Values["admin-password"] = "wrong"
	err := AdminSudo(x)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("got %v", err)
	}
	tries := 0
	for _, s := range fake.Ran {
		if strings.Contains(s.String(), "sudo -S") {
			tries++
			if strings.Count(s.Stdin, "\n") != 1 {
				t.Errorf("more than one line for sudo to try: %q", s.Stdin)
			}
		}
	}
	if tries != 1 || x.Value("admin_password") != "" || x.Value("admin_prelude") != "" {
		t.Fatalf("%d tries, password kept %v, prelude %q", tries, x.Value("admin_password") != "", x.Value("admin_prelude"))
	}
}

func TestASudoPasswordIsNeededWithoutATerminalSaysHowToGiveIt(t *testing.T) {
	x, _, _ := adminCtx(t, engine.Options{Yes: true}, sudoHost(true, "s3cret"))
	err := AdminSudo(x)
	if err == nil || !strings.Contains(err.Error(), "--admin-password-stdin") {
		t.Fatalf("got %v", err)
	}
}
