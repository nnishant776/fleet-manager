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
	daveKey    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx"
	maintainer = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMAINTAINERMAINTAINERMAINTAINERMAINTAINERMA"
)

// ownRepo is what the fake git and sops answer from for a user's own commands.
type ownRepo struct {
	local, remote bool              // user/dave exists in the clone, on origin
	diverged      bool              // the local branch is not an ancestor of origin's
	files         map[string]string // host -> the user's file there, absent when not a key
	stored        map[string]string // sops path -> the value stored, for -d --extract
	badSignature  bool              // the branch's newest commit is signed by no one trusted
	signersSeen   string            // the allowed signers git was given
}

func newOwnRepo() *ownRepo {
	return &ownRepo{local: true, remote: true, files: map[string]string{"nas": "x", "pi": "x"}, stored: map[string]string{}}
}

func (r *ownRepo) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	fail := engine.Result{Code: 1}
	switch s.Name {
	case "mkpasswd":
		return engine.Result{Stdout: "$y$j9T$salt$hash\n"}, nil
	case "age":
		return engine.Result{Stdout: "AGE-SECRET-KEY-1TEST\n"}, nil
	case "sops":
		if s.Args[0] == "-d" {
			path := s.Args[2]
			if v, ok := r.stored[path]; ok {
				return engine.Result{Stdout: v}, nil
			}
			return engine.Result{Code: 1, Stderr: "component " + path + " not found"}, errors.New("exit 1")
		}
	case "git":
		switch {
		case strings.Contains(args, "verify-commit"):
			for _, a := range s.Args {
				if p, ok := strings.CutPrefix(a, "gpg.ssh.allowedSignersFile="); ok {
					b, _ := os.ReadFile(p)
					r.signersSeen = string(b)
				}
			}
			if r.badSignature {
				return fail, errors.New("exit 1")
			}
		case (strings.Contains(args, "rev-parse --verify -q refs/heads/user/") && !strings.HasSuffix(args, "^{commit}")):
			if !r.local {
				return fail, errors.New("exit 1")
			}
		case (strings.Contains(args, "rev-parse --verify -q refs/remotes/origin/user/") && !strings.HasSuffix(args, "^{commit}")):
			if !r.remote {
				return fail, errors.New("exit 1")
			}
		case strings.Contains(args, "rev-parse refs/heads/"):
			return engine.Result{Stdout: "1111\n"}, nil
		case strings.Contains(args, "rev-parse refs/remotes/"):
			return engine.Result{Stdout: "2222\n"}, nil
		case strings.Contains(args, "merge-base --is-ancestor"):
			if r.diverged {
				return fail, errors.New("exit 1")
			}
		case strings.Contains(args, "cat-file -e"):
			ref := s.Args[len(s.Args)-1]
			_, path, _ := strings.Cut(ref, ":")
			h := strings.TrimSuffix(strings.TrimPrefix(path, "users/dave/"), ".yaml")
			if _, ok := r.files[h]; !ok {
				return fail, errors.New("exit 1")
			}
		case s.Args[0] == "ls-tree":
			var lines []string
			for h := range r.files {
				lines = append(lines, "users/dave/"+h+".yaml")
			}
			return engine.Result{Stdout: strings.Join(lines, "\n") + "\n"}, nil
		}
	}
	return engine.Result{}, nil
}

// runOwn runs a user command as dave, with an enrollment file, a signers
// file, and (unless the flags say otherwise) his identity.
func runOwn(t *testing.T, f *engine.Flow, r *ownRepo, flags map[string]string) (error, *engine.Fake, string) {
	t.Helper()
	home := t.TempDir()
	enroll := filepath.Join(home, "dave.enroll")
	if err := os.WriteFile(enroll, []byte(userEnrollDoc("dave", "age1daverecipient", daveKey+" dave@laptop")), 0o644); err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(home, "allowed_signers")
	if err := os.WriteFile(signers, []byte("maintainer namespaces=\"git\" "+maintainer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forgetTrusted(t)
	x, fake, out := newFakeCtx(r.respond)
	x.Stdin = strings.NewReader("the secret")
	args := map[string]string{
		"repo": t.TempDir(), "user": "dave", "identity": filepath.Join(home, "user.age"), "enroll": enroll,
		"signing-key": filepath.Join(home, "fleet_user_signing_key"), "signers": signers, "flake": "",
	}
	for k, v := range flags {
		args[k] = v
	}
	err := engine.RunWith(x, f, args)
	return err, fake, out.String()
}

func TestUserPasswordChangesTheFilesOfTheHostsAndPushesTheBranch(t *testing.T) {
	r := newOwnRepo()
	err, fake, out := runOwn(t, UserPassword, r, map[string]string{"host": "pi\nnas", "password": "hunter2"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"git fetch -q origin +refs/heads/user/dave:refs/remotes/origin/user/dave",
		"git -c gpg.ssh.allowedSignersFile=",
		"age -d ",
		"git worktree add -q "+filepath.Join(os.TempDir(), "fleet-secrets-"),
		`sops set --value-stdin users/dave/nas.yaml '["password"]'`,
		`sops set --value-stdin users/dave/pi.yaml '["password"]'`,
		"git add users/dave/nas.yaml users/dave/pi.yaml",
		"-c gpg.format=ssh -c user.signingKey=",
		"fleet_user_signing_key.pub -c commit.gpgSign=true commit -q -m 'user-password: dave on nas, pi'",
		"git worktree remove --force",
		"git push origin user/dave",
	)
	for _, s := range fake.Ran {
		if s.Name == "mkpasswd" && s.Stdin != "hunter2" {
			t.Errorf("mkpasswd got %q", s.Stdin)
		}
		if s.Name == "sops" && s.Args[0] == "set" && s.Stdin != `"$y$j9T$salt$hash"` {
			t.Errorf("stored %q", s.Stdin)
		}
		// The unlocked identity reaches sops and nothing else.
		if s.Name != "sops" && len(s.Hidden) > 0 {
			t.Errorf("%s got the identity", s.Name)
		}
		if s.Name == "sops" && (len(s.Hidden) != 1 || s.Hidden[0] != "SOPS_AGE_KEY=AGE-SECRET-KEY-1TEST") {
			t.Errorf("sops got %v", s.Hidden)
		}
	}
	// Judged by his own key and the signers, nothing else.
	if !strings.Contains(r.signersSeen, "dave namespaces=\"git\" "+daveKey) || !strings.Contains(r.signersSeen, maintainer) {
		t.Errorf("git checked against\n%s", r.signersSeen)
	}
}

func TestUserEditRefusesABranchNeitherHisKeyNorTheSignersSigned(t *testing.T) {
	r := newOwnRepo()
	r.badSignature = true
	err, fake, out := runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x"})
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "signed neither by your signing key nor by a key in your copy") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "sops") || strings.Contains(c, "worktree add") || strings.Contains(c, "push") {
			t.Errorf("ran %s after a bad signature", c)
		}
	}
}

func TestUserEditNeedsHostsOrAllHostsAndAFileOnEach(t *testing.T) {
	for _, flags := range []map[string]string{
		{},
		{"host": "nas", "all-hosts": "true"},
	} {
		if err, fake, out := runOwn(t, UserPassword, newOwnRepo(), flags); err == nil {
			t.Errorf("%v was accepted\n%s", flags, out)
		} else if strings.Contains(strings.Join(fake.Commands(), "\n"), "sops set") {
			t.Errorf("changed something with %v", flags)
		}
	}
	err, _, out := runOwn(t, UserPassword, newOwnRepo(), map[string]string{"host": "mars", "password": "x"})
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "has no users/dave/mars.yaml") {
		t.Errorf("got %v\n%s", err, out)
	}
}

func TestUserPasswordForAllHostsTakesEveryFileOnTheBranch(t *testing.T) {
	err, fake, out := runOwn(t, UserPassword, newOwnRepo(), map[string]string{"all-hosts": "true", "password": "x"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake, "git ls-tree --name-only refs/heads/user/dave users/dave/",
		"users/dave/nas.yaml", "users/dave/pi.yaml", "commit -q -m 'user-password: dave on nas, pi'")
}

func TestUserEditBringsALocalBranchUpToOriginsOrKeepsItWhenItHasMore(t *testing.T) {
	r := newOwnRepo()
	_, fake, out := runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x"})
	inOrder(t, fake, "git branch -f -q user/dave refs/remotes/origin/user/dave")
	r = newOwnRepo()
	r.diverged = true
	err, fake, out := runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(strings.Join(fake.Commands(), "\n"), "branch -f") || !strings.Contains(out, "has commits origin's lacks") {
		t.Errorf("a diverged branch was moved or not mentioned:\n%s", out)
	}
	r = newOwnRepo()
	r.local = false
	_, fake, _ = runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x"})
	inOrder(t, fake, "git worktree add -q -b user/dave", "refs/remotes/origin/user/dave")
	r = newOwnRepo()
	r.local, r.remote = false, false
	if err, _, out := runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x"}); err == nil || !strings.Contains(err.Error()+out, "there is no user/dave") {
		t.Errorf("a missing branch: %v\n%s", err, out)
	}
}

func TestUserKeysAddMakesNoCommitWhenTheKeyIsThereAlready(t *testing.T) {
	r := newOwnRepo()
	r.stored[`["authorized_keys"]`] = "ssh-ed25519 " + keyOne + " one\n"
	add := writeKeys(t, "ssh-ed25519 "+keyOne+" again\n")
	err, fake, out := runOwn(t, UserKeys, r, map[string]string{"host": "nas", "add-authorized-keys": add})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, no := range []string{"sops set", "commit -q", "push"} {
		if strings.Contains(cmds, no) {
			t.Errorf("ran %q although nothing changed:\n%s", no, cmds)
		}
	}
	if !strings.Contains(cmds, "git worktree remove") {
		t.Errorf("the worktree was not removed:\n%s", cmds)
	}
}

func TestUserKeysAddsAKeyAndLeavesTheRest(t *testing.T) {
	r := newOwnRepo()
	r.stored[`["authorized_keys"]`] = "# mine\nssh-ed25519 " + keyOne + " one\n"
	add := writeKeys(t, "ssh-ed25519 "+keyNew+" new\n")
	err, fake, out := runOwn(t, UserKeys, r, map[string]string{"host": "nas\npi", "add-authorized-keys": add})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake, `sops set --value-stdin users/dave/nas.yaml '["authorized_keys"]'`,
		`sops set --value-stdin users/dave/pi.yaml '["authorized_keys"]'`, "commit -q -m 'user-keys: dave on nas, pi'", "git push origin user/dave")
	for _, s := range fake.Ran {
		if s.Name == "sops" && s.Args[0] == "set" && !strings.Contains(s.Stdin, `# mine\nssh-ed25519 `+keyOne+` one\nssh-ed25519 `+keyNew+` new`) {
			t.Errorf("stored %q", s.Stdin)
		}
	}
}

func TestUserKeysReplaceMakesNoCommitForTheSameKeys(t *testing.T) {
	r := newOwnRepo()
	r.stored[`["sudo_keys"]`] = "ssh-ed25519 " + keyOne + " one\n"
	same := writeKeys(t, "ssh-ed25519 "+keyOne+" one\n")
	err, fake, out := runOwn(t, UserKeys, r, map[string]string{"host": "nas", "sudo-keys": same})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cmds := strings.Join(fake.Commands(), "\n"); strings.Contains(cmds, "sops set") || strings.Contains(cmds, "commit -q") {
		t.Errorf("changed the same keys:\n%s", cmds)
	}
	other := writeKeys(t, "ssh-ed25519 "+keyNew+" new\n")
	err, fake, out = runOwn(t, UserKeys, r, map[string]string{"host": "nas", "sudo-keys": other})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake, `sops set --value-stdin users/dave/nas.yaml '["sudo_keys"]'`, "commit -q -m 'user-keys: dave on nas'")
}

func TestUserSecretSetStoresUnderSecretsAndSkipsTheSameValue(t *testing.T) {
	r := newOwnRepo()
	err, fake, out := runOwn(t, UserSecretSet, r, map[string]string{"name": "token", "host": "nas", "value": "s3cret"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake, `sops set --value-stdin users/dave/nas.yaml '["secrets"]["token"]'`, "commit -q -m 'user-secret: set token for dave on nas'", "git push origin user/dave")
	for _, s := range fake.Ran {
		if s.Name == "sops" && s.Args[0] == "set" && s.Stdin != `"s3cret"` {
			t.Errorf("stored %q", s.Stdin)
		}
	}
	r.stored[`["secrets"]["token"]`] = "s3cret"
	_, fake, out = runOwn(t, UserSecretSet, r, map[string]string{"name": "token", "host": "nas", "value": "s3cret"})
	if cmds := strings.Join(fake.Commands(), "\n"); strings.Contains(cmds, "sops set") || strings.Contains(cmds, "commit -q") {
		t.Errorf("stored the same value again:\n%s\n%s", cmds, out)
	}
	if err, _, _ := runOwn(t, UserSecretSet, r, map[string]string{"name": "to/ken", "host": "nas", "value": "x"}); err == nil {
		t.Error("a name with a slash was accepted")
	}
}

func TestUserSecretUnsetRemovesOnlyWhereItIs(t *testing.T) {
	r := newOwnRepo()
	r.stored[`["secrets"]["token"]`] = "s3cret"
	err, fake, out := runOwn(t, UserSecretUnset, r, map[string]string{"name": "token", "host": "nas"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake, `sops unset users/dave/nas.yaml '["secrets"]["token"]'`, "commit -q -m 'user-secret: unset token for dave on nas'", "git push origin user/dave")
	delete(r.stored, `["secrets"]["token"]`)
	_, fake, _ = runOwn(t, UserSecretUnset, r, map[string]string{"name": "token", "all-hosts": "true"})
	if cmds := strings.Join(fake.Commands(), "\n"); strings.Contains(cmds, "sops unset") || strings.Contains(cmds, "commit -q") {
		t.Errorf("removed what was not there:\n%s", cmds)
	}
}

func TestUserEditFallsBackToTheSigningKeyWhenThereIsNoEnrollmentFile(t *testing.T) {
	r := newOwnRepo()
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	_ = os.WriteFile(key+".pub", []byte(daveKey+" dave@laptop\n"), 0o644)
	err, _, out := runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x", "enroll": filepath.Join(dir, "none"), "signing-key": key})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(r.signersSeen, daveKey) {
		t.Errorf("git checked against\n%s", r.signersSeen)
	}
	err, _, out = runOwn(t, UserPassword, r, map[string]string{"host": "nas", "password": "x", "enroll": filepath.Join(dir, "none"), "signing-key": filepath.Join(dir, "absent")})
	if err == nil || !strings.Contains(err.Error()+out, "run fleet user init first") {
		t.Errorf("got %v\n%s", err, out)
	}
}
