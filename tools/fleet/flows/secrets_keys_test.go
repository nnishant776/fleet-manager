package flows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx alice@laptop"
	keyB = "sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIB support"
)

func writeKeys(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runUserKeys runs user-keys for alice on nas with the given flags; bad is a
// line the fake ssh-keygen refuses.
func runUserKeys(t *testing.T, flags map[string]string, bad string) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if res, ok := hostFile(s); ok {
			return res, nil
		}
		if s.Name == "ssh-keygen" && bad != "" && strings.Contains(s.Stdin, bad) {
			return engine.Result{Code: 255}, os.ErrInvalid
		}
		return engine.Result{}, nil
	})
	x.Stdin = strings.NewReader("the passphrase")
	args := map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice", "host": "nas"}
	for k, v := range flags {
		args[k] = v
	}
	err := engine.RunWith(x, SecretsUserKeys, args)
	return err, fake, out.String()
}

func TestUserKeysStoresBothFilesCommitsAndPushes(t *testing.T) {
	login := writeKeys(t, "# alice's keys\n"+keyA+"\n\nfrom=\"192.168.0.0/24\" "+keyB+"\n")
	sudo := writeKeys(t, keyB+"\n")
	err, fake, out := runUserKeys(t, map[string]string{"authorized-keys": login, "sudo-keys": sudo}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, want := range []string{
		"sops --encrypt --age age1hostrecipient,age1recoveryrecipient",
		`sops set --value-stdin users/alice/nas.yaml '["authorized_keys"]'`,
		`sops set --value-stdin users/alice/nas.yaml '["sudo_keys"]'`,
		"git commit -q -m 'user-keys: alice on nas'",
		"git push origin host/nas",
	} {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
	lines := 0
	for _, s := range fake.Ran {
		if s.Name == "ssh-keygen" {
			lines++
		}
	}
	if lines != 3 {
		t.Errorf("ssh-keygen checked %d lines, want 3 (comment and blank lines are not keys):\n%s", lines, cmds)
	}
	// The whole file is the value, as one JSON string.
	var stored string
	for _, s := range fake.Ran {
		if s.Name == "sops" && strings.Contains(s.String(), "authorized_keys") {
			stored = s.Stdin
		}
	}
	if !strings.Contains(stored, `alice@laptop\n\nfrom=\"192.168.0.0/24\" sk-ssh`) {
		t.Errorf("the value stored is %q", stored)
	}
}

func TestUserKeysSetsOnlyTheFileGiven(t *testing.T) {
	err, fake, out := runUserKeys(t, map[string]string{"sudo-keys": writeKeys(t, keyA+"\n")}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	if strings.Contains(cmds, "authorized_keys") || !strings.Contains(cmds, `["sudo_keys"]`) {
		t.Errorf("wrong keys set:\n%s", cmds)
	}
}

func TestUserKeysRefusesALineThatIsNotAKey(t *testing.T) {
	file := writeKeys(t, keyA+"\nssh-ed25519 AAAA-typo\n")
	err, fake, out := runUserKeys(t, map[string]string{"authorized-keys": file}, "AAAA-typo")
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "line 2 is not an SSH public key") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git") || strings.HasPrefix(c, "sops") {
			t.Errorf("ran %s after a bad key", c)
		}
	}
}

func TestUserKeysRefusesNothingToStore(t *testing.T) {
	if err, _, out := runUserKeys(t, nil, ""); err == nil {
		t.Errorf("no file given was accepted\n%s", out)
	}
	err, _, out := runUserKeys(t, map[string]string{"authorized-keys": writeKeys(t, "# nobody\n\n")}, "")
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "holds no keys") {
		t.Errorf("got %v\n%s", err, out)
	}
}

const (
	keyOne = "AAAAC3NzaC1lZDI1NTE5AAAAIONEONEONEONEONEONEONEONEONEONEONEONEONEONE"
	keyTwo = "AAAAC3NzaC1lZDI1NTE5AAAAITWOTWOTWOTWOTWOTWOTWOTWOTWOTWOTWOTWOTWOTWOTWO"
	keyNew = "AAAAC3NzaC1lZDI1NTE5AAAAINEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEWNEW"
)

func TestMergeKeysAddsOnlyWhatIsNotThereAndKeepsTheRest(t *testing.T) {
	current := "# maintainers\nfrom=\"10.0.0.0/8\" ssh-ed25519 " + keyOne + " one\nssh-ed25519 " + keyTwo + " two"
	got, err := mergeKeys(current, []string{"ssh-ed25519 " + keyOne + " again", "ssh-ed25519 " + keyNew + " new", "ssh-ed25519 " + keyNew + " new"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := current + "\nssh-ed25519 " + keyNew + " new"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMergeKeysRemovesByMaterialWhateverTheOptionsAndComment(t *testing.T) {
	current := "# maintainers\nfrom=\"10.0.0.0/8\" ssh-ed25519 " + keyOne + " other-comment\nssh-ed25519 " + keyTwo + " two"
	got, err := mergeKeys(current, nil, []string{"ssh-ed25519 " + keyOne + " old"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "# maintainers\nssh-ed25519 " + keyTwo + " two"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMergeKeysRefusesToLeaveNoKey(t *testing.T) {
	_, err := mergeKeys("# c\nssh-ed25519 "+keyOne+" one\n", nil, []string{"ssh-ed25519 " + keyOne})
	if err == nil || !strings.Contains(err.Error(), "lock the account out") {
		t.Errorf("got %v", err)
	}
	if _, err := mergeKeys("", nil, []string{"ssh-ed25519 " + keyOne}); err == nil {
		t.Error("removing from nothing was accepted")
	}
}

// runMerge runs user-keys with the flags, the stored authorized_keys being
// current ("" for a file without that key).
func runMerge(t *testing.T, current string, flags map[string]string) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if res, ok := hostFile(s); ok {
			return res, nil
		}
		if s.Name == "sops" && len(s.Args) > 0 && s.Args[0] == "-d" {
			if current == "" {
				return engine.Result{Code: 1, Stderr: "component ['authorized_keys'] not found"}, os.ErrInvalid
			}
			return engine.Result{Stdout: current}, nil
		}
		return engine.Result{}, nil
	})
	args := map[string]string{"repo": t.TempDir(), "recovery": "", "user": "support", "host": "nas"}
	for k, v := range flags {
		args[k] = v
	}
	err := engine.RunWith(x, SecretsUserKeys, args)
	return err, fake, out.String()
}

func TestUserKeysAddMergesIntoWhatIsStored(t *testing.T) {
	current := "ssh-ed25519 " + keyOne + " one\n"
	err, fake, out := runMerge(t, current, map[string]string{"add-authorized-keys": writeKeys(t, "from=\"10.0.0.0/8\" ssh-ed25519 "+keyNew+" new\n")})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, want := range []string{"sops -d --extract", "sops set --value-stdin users/support/nas.yaml '[\"authorized_keys\"]'", "git commit -q -m 'user-keys: support on nas'", "git push origin host/nas"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
	for _, s := range fake.Ran {
		if s.Name == "sops" && len(s.Args) > 0 && s.Args[0] == "set" && !strings.Contains(s.Stdin, `one\nfrom=\"10.0.0.0/8\" ssh-ed25519 `+keyNew) {
			t.Errorf("stored %q", s.Stdin)
		}
	}
}

func TestUserKeysAddOfAKeyAlreadyThereMakesNoCommit(t *testing.T) {
	current := "ssh-ed25519 " + keyOne + " one\n"
	err, fake, out := runMerge(t, current, map[string]string{"add-authorized-keys": writeKeys(t, "ssh-ed25519 "+keyOne+" renamed\n")})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "git push") || strings.HasPrefix(c, "sops set") {
			t.Errorf("ran %s though nothing changed", c)
		}
	}
	if !strings.Contains(out, "nothing to add or remove") || !strings.Contains(out, "worktree is removed") && !strings.Contains(strings.Join(fake.Commands(), "\n"), "worktree remove") {
		t.Errorf("did not say so, or left the worktree:\n%s", out)
	}
}

func TestUserKeysRemoveToNothingIsRefusedAndAMissingKeyCountsAsEmpty(t *testing.T) {
	err, fake, out := runMerge(t, "ssh-ed25519 "+keyOne+" one\n", map[string]string{"remove-authorized-keys": writeKeys(t, "ssh-ed25519 "+keyOne+" x\n")})
	if err == nil || !strings.Contains(out, "lock the account out") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "sops set") {
			t.Errorf("ran %s", c)
		}
	}
	// With no key stored, adding one works.
	if err, _, out := runMerge(t, "", map[string]string{"add-authorized-keys": writeKeys(t, "ssh-ed25519 "+keyNew+" n\n")}); err != nil {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestUserKeysReplacingAndChangingExcludeEachOther(t *testing.T) {
	f := writeKeys(t, "ssh-ed25519 "+keyOne+" one\n")
	for _, other := range []string{"add-authorized-keys", "remove-authorized-keys"} {
		err, fake, out := runMerge(t, "", map[string]string{"authorized-keys": f, other: f})
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "give one or the other") {
			t.Errorf("%s: got %v\n%s", other, err, out)
		}
		for _, c := range fake.Commands() {
			if strings.HasPrefix(c, "git") {
				t.Errorf("ran %s", c)
			}
		}
	}
	// The sudo pair is its own: a replaced login list and an added sudo key may go together.
	if err, _, out := runMerge(t, "", map[string]string{"authorized-keys": f, "add-sudo-keys": f}); err != nil {
		t.Errorf("%v\n%s", err, out)
	}
	if err, _, _ := runMerge(t, "", map[string]string{"sudo-keys": f, "remove-sudo-keys": f}); err == nil {
		t.Error("--sudo-keys with --remove-sudo-keys was accepted")
	}
}
