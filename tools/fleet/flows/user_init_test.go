package flows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// runUserInit runs user init for dave in a home folder of its own; the fake
// stands in for age, age-keygen and ssh-keygen.
func runUserInit(t *testing.T, home string) (error, *engine.Fake, string) {
	t.Helper()
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		switch {
		case s.Name == "ssh-keygen":
			_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
			keygen(s)
		case s.Name == "sh" && strings.Contains(strings.Join(s.Args, " "), "age-keygen -y"):
			return engine.Result{Stdout: "age1daverecipient\n"}, nil
		}
		return engine.Result{}, nil
	})
	err := engine.RunWith(x, UserInit, map[string]string{
		"user": "dave", "identity": filepath.Join(home, ".fleet", "user.age"),
		"signing-key": filepath.Join(home, ".ssh", "fleet_user_signing_key"),
		"enroll":      filepath.Join(home, ".fleet", "dave.enroll"),
	})
	return err, fake, out.String()
}

func TestUserInitMakesTheIdentityTheKeyAndTheEnrollmentFile(t *testing.T) {
	home := t.TempDir()
	err, fake, out := runUserInit(t, home)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, want := range []string{
		`age-keygen | age -p -o "$1"' sh ` + filepath.Join(home, ".fleet", "user.age"),
		"ssh-keygen -t ed25519 -a 100 -C 'dave fleet signing' -f " + filepath.Join(home, ".ssh", "fleet_user_signing_key"),
		`age -d "$1" | age-keygen -y' sh ` + filepath.Join(home, ".fleet", "user.age"),
	} {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
	doc, err := os.ReadFile(filepath.Join(home, ".fleet", "dave.enroll"))
	if err != nil {
		t.Fatal(err)
	}
	want := "version: 1\nuser: dave\nage_recipient: age1daverecipient\nsigning_key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx\n"
	if string(doc) != want {
		t.Errorf("the enrollment file is\n%s\nwant\n%s", doc, want)
	}
	if !strings.Contains(out, "fleet secrets user-enroll dave --from dave.enroll") {
		t.Errorf("it does not say what to give the maintainer:\n%s", out)
	}
	// The identity is never written in clear by fleet itself.
	if fileExists(filepath.Join(home, ".fleet", "user.age")) {
		t.Error("fleet wrote the identity file itself; age should")
	}
}

func TestUserInitSkipsWhatIsDone(t *testing.T) {
	home := t.TempDir()
	for _, f := range []string{".fleet/user.age", ".ssh/fleet_user_signing_key", ".fleet/dave.enroll"} {
		p := filepath.Join(home, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte("x\n"), 0o600)
	}
	err, fake, out := runUserInit(t, home)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(fake.Ran) != 0 {
		t.Errorf("ran %v although everything was done", fake.Commands())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".fleet", "dave.enroll")); string(b) != "x\n" {
		t.Errorf("the enrollment file was rewritten: %q", b)
	}
}

func TestUserInitRefusesAnotherUsersName(t *testing.T) {
	x, _, out := newFakeCtx(func(engine.Spec) (engine.Result, error) { return engine.Result{}, nil })
	if err := engine.RunWith(x, UserInit, map[string]string{"user": "Dave Smith"}); err == nil {
		t.Errorf("a name with a space and capitals was accepted\n%s", out.String())
	}
}
