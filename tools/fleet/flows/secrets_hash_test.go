package flows

import (
	"strings"
	"testing"
)

const aHash = "$y$j9T$abcdefgh$ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"

func TestUserPasswordHashStdinStoresTheHashAsItIs(t *testing.T) {
	r := listedAlice()
	r.userFiles["nas"] = false
	err, fake, out := runUserFlow(t, SecretsUserPassword, r, map[string]string{"hash": aHash}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var stored bool
	for _, s := range fake.Ran {
		if s.Name == "mkpasswd" {
			t.Errorf("hashed a password anyway: %s", s)
		}
		for _, a := range s.Args {
			if strings.Contains(a, aHash) {
				t.Errorf("the hash is in an argument of %s", s)
			}
		}
		if s.Name == "sops" && len(s.Args) > 0 && s.Args[0] == "set" {
			stored = s.Stdin == `"`+aHash+`"`
		}
	}
	if !stored {
		t.Errorf("sops set was not given the hash as it is on stdin:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	if strings.Contains(out, aHash) {
		t.Errorf("showed the hash:\n%s", out)
	}
	if !strings.Contains(out, "a hash was given") {
		t.Errorf("did not say the hashing was skipped:\n%s", out)
	}
	inOrder(t, fake, `sops set --value-stdin users/alice/nas.yaml '["password"]'`, "git commit -q -m 'user-password: alice@nas'")
}

func TestUserPasswordRefusesAHashThatIsNotOne(t *testing.T) {
	for _, bad := range []string{"plaintext", "$y$", "a b", "*", "$6$abc def"} {
		r := listedAlice()
		err, fake, out := runUserFlow(t, SecretsUserPassword, r, map[string]string{"hash": bad}, nil)
		if err == nil || !strings.Contains(err.Error()+out, "does not match") {
			t.Errorf("%q: went ahead, or without saying why: %v\n%s", bad, err, out)
		}
		if len(fake.Ran) > 0 && strings.Contains(strings.Join(fake.Commands(), "\n"), "sops set") {
			t.Errorf("%q: stored it", bad)
		}
	}
	// A locked account's hash is a hash too.
	if err, _, out := runUserFlow(t, SecretsUserPassword, listedAlice(), map[string]string{"hash": "!"}, nil); err != nil {
		t.Errorf("refused !: %v\n%s", err, out)
	}
}

func TestUserPasswordTypedPasswordIsStillHashed(t *testing.T) {
	r := listedAlice()
	err, fake, out := runUserFlow(t, SecretsUserPassword, r, map[string]string{"password": "pw"}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !ran(fake, "mkpasswd -m yescrypt --stdin") {
		t.Errorf("did not hash the password:\n%s", strings.Join(fake.Commands(), "\n"))
	}
}
