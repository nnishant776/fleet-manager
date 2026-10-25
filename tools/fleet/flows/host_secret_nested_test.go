package flows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// cacheRecovery gives the test the recovery recipient fleet setup caches, and a
// temporary folder of its own, where the commands make their worktrees.
func cacheRecovery(t *testing.T) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("TMPDIR", t.TempDir())
	_ = os.MkdirAll(filepath.Join(state, "fleet"), 0o755)
	_ = os.WriteFile(filepath.Join(state, "fleet", "recovery.pub"), []byte("age1recoveryrecipient\n"), 0o644)
}

// hostFile answers yq for the recipients of a host's file on its branch.
func hostFile(s engine.Spec) (engine.Result, bool) {
	if s.Name == "yq" && strings.HasPrefix(strings.Join(s.Args, " "), ".sops.age") {
		return engine.Result{Stdout: "age1hostrecipient\nage1recoveryrecipient\n"}, true
	}
	return engine.Result{}, false
}

// runHostSecret stores value under name for host nas, in a secrets file
// shaped like doc (its JSON, as yq prints it).
func runHostSecret(t *testing.T, name, doc string) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if res, ok := hostFile(s); ok {
			return res, nil
		}
		if s.Name == "yq" && strings.HasPrefix(args, "-o=json") {
			return engine.Result{Stdout: doc}, nil
		}
		return engine.Result{}, nil
	})
	x.Stdin = strings.NewReader("the passphrase")
	err := engine.RunWith(x, HostSecret, map[string]string{
		"repo": t.TempDir(), "recovery": "", "host": "nas", "name": name, "value-stdin": "true",
	})
	return err, fake, out.String()
}

func TestHostSecretNestsOnSlash(t *testing.T) {
	err, fake, out := runHostSecret(t, "hotspot/wls0", `{"hotspot": {"wls1": "ENC[…]"}, "sops": {}}`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, want := range []string{
		`sops set --value-stdin hosts/nas.yaml '["hotspot"]["wls0"]'`,
		"git commit -q -m 'nas: hotspot/wls0'",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
	for _, want := range []string{`sops.secrets."hotspot/wls0"`, "/run/secrets/hotspot/wls0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "the passphrase") {
		t.Errorf("the value was shown:\n%s", out)
	}
}

func TestHostSecretRefusesToReplaceAGroup(t *testing.T) {
	doc := `{"hotspot": {"wls0": "ENC[…]", "wls1": "ENC[…]"}, "sops": {}}`
	for name, want := range map[string]string{
		"hotspot":           "hotspot holds secrets of its own (wls0, wls1)",
		"hotspot/wls0/more": "hotspot/wls0 holds a value",
	} {
		err, fake, out := runHostSecret(t, name, doc)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), want) {
			t.Errorf("%s: got %v, want %q\n%s", name, err, want, out)
		}
		for _, c := range fake.Commands() {
			if strings.HasPrefix(c, "sops set") || strings.HasPrefix(c, "git commit") {
				t.Errorf("%s: ran %s", name, c)
			}
		}
	}
}

// A run that stops after the nested write, here at a failed commit, takes the
// worktree it made away again, with the host's file in it.
func TestHostSecretRemovesTheWorktreeWhenItStops(t *testing.T) {
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if res, ok := hostFile(s); ok {
			return res, nil
		}
		switch {
		case s.Name == "yq" && strings.HasPrefix(args, "-o=json"):
			return engine.Result{Stdout: `{"sops": {}}`}, nil
		case s.Name == "git" && strings.HasPrefix(args, "commit"):
			return engine.Result{Code: 1, Stderr: "error: gpg failed to sign the data"}, errors.New("exited with code 1")
		}
		return engine.Result{}, nil
	})
	x.Stdin = strings.NewReader("the passphrase")
	err := engine.RunWith(x, HostSecret, map[string]string{
		"repo": t.TempDir(), "recovery": "", "host": "nas", "name": "hotspot/wls0", "value-stdin": "true",
	})
	if err == nil {
		t.Fatalf("the failed commit was not reported\n%s", out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	set := strings.Index(cmds, `sops set --value-stdin hosts/nas.yaml '["hotspot"]["wls0"]'`)
	remove := strings.Index(cmds, "worktree remove --force")
	if set < 0 || remove < set {
		t.Errorf("the worktree was not removed after the nested write:\n%s\n%s", cmds, out)
	}
	if strings.Contains(cmds, "git push") {
		t.Errorf("pushed after the failed commit:\n%s", cmds)
	}
}
