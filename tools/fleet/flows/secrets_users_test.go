package flows

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

const (
	aliceAge = "age1alicerecipient"
	aliceKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx alice@laptop"
)

// userRepo is what the fake git and sops answer from: which branches exist,
// which hosts hold a file of alice's, which list her, and her enroll file.
type userRepo struct {
	hosts      []string          // host branches
	hostFile   map[string]bool   // host -> its branch holds users/alice/<host>.yaml
	listed     map[string]string // host -> the identity (JSON) its file lists alice with
	userBranch bool              // user/alice exists
	userFiles  map[string]bool   // host -> user/alice holds users/alice/<host>.yaml
	enroll     string            // user/alice's enroll file
	badEncrypt bool              // the moved file is encrypted to a stranger as well
}

func userEnrollDoc(user, recipient, key string) string {
	return "version: 1\nuser: " + user + "\nage_recipient: " + recipient + "\nsigning_key: " + key + "\n"
}

func (r *userRepo) respond(s engine.Spec) (engine.Result, error) {
	args := strings.Join(s.Args, " ")
	if res, ok := hostFile(s); ok && len(s.Args) == 1 {
		return res, nil
	}
	switch s.Name {
	case "yq":
		switch {
		case strings.HasPrefix(args, ".sops.age"): // the moved file's recipients
			out := "age1hostrecipient\n" + aliceAge + "\nage1recoveryrecipient\n"
			if r.badEncrypt {
				out += "age1stranger\n"
			}
			return engine.Result{Stdout: out}, nil
		case strings.HasPrefix(args, ".users["):
			// The host's file is on stdin; its first line says which host.
			host := strings.TrimSpace(strings.SplitN(s.Stdin, "\n", 2)[0])
			if _, ok := r.listed[host]; ok {
				return engine.Result{Stdout: "true\n"}, nil
			}
			return engine.Result{Stdout: "false\n"}, nil
		}
		doc := map[string]string{}
		for _, line := range strings.Split(s.Stdin, "\n") {
			if k, v, ok := strings.Cut(line, ": "); ok {
				doc[k] = v
			}
		}
		return engine.Result{Stdout: doc[strings.TrimPrefix(args, ".")] + "\n"}, nil
	case "sops":
		if s.Args[0] == "-d" { // the identity a host's file lists alice with
			host := strings.TrimSpace(strings.SplitN(s.Stdin, "\n", 2)[0])
			return engine.Result{Stdout: r.listed[host]}, nil
		}
		return engine.Result{Stdout: "ENCRYPTED"}, nil
	case "git":
		switch {
		case strings.HasPrefix(args, "for-each-ref"):
			var refs []string
			for _, h := range r.hosts {
				refs = append(refs, "refs/heads/host/"+h)
			}
			return engine.Result{Stdout: strings.Join(refs, "\n")}, nil
		case strings.Contains(args, "rev-parse --verify -q refs/heads/user/"):
			if !r.userBranch {
				return engine.Result{Code: 1}, errors.New("exit 1")
			}
		case strings.Contains(args, "rev-parse --verify -q refs/remotes/origin/user/"):
			return engine.Result{Code: 1}, errors.New("exit 1")
		case strings.Contains(args, "cat-file -e"):
			ref := s.Args[len(s.Args)-1]
			ref, path, _ := strings.Cut(ref, ":")
			if strings.HasPrefix(ref, "refs/heads/user/") {
				h := strings.TrimSuffix(strings.TrimPrefix(path, "users/alice/"), ".yaml")
				if !r.userFiles[h] {
					return engine.Result{Code: 1}, errors.New("exit 1")
				}
			} else if !r.hostFile[strings.TrimSuffix(strings.TrimPrefix(path, "users/alice/"), ".yaml")] {
				return engine.Result{Code: 1}, errors.New("exit 1")
			}
		case s.Args[0] == "show" && strings.HasSuffix(args, ":enroll"):
			return engine.Result{Stdout: r.enroll}, nil
		case s.Args[0] == "show" && strings.Contains(args, ":hosts/"):
			host := strings.TrimSuffix(args[strings.Index(args, ":hosts/")+len(":hosts/"):], ".yaml")
			return engine.Result{Stdout: host + "\nsops: {}\n"}, nil
		case s.Args[0] == "show":
			return engine.Result{Stdout: "ENCRYPTED OLD"}, nil
		}
	}
	return engine.Result{}, nil
}

func runUserEnroll(t *testing.T, r *userRepo, doc string, hosts ...string) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	from := filepath.Join(t.TempDir(), "alice.enroll")
	if err := os.WriteFile(from, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	x, fake, out := newFakeCtx(r.respond)
	args := map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice", "from": from, "host": strings.Join(hosts, "\n")}
	err := engine.RunWith(x, SecretsUserEnroll, args)
	return err, fake, out.String()
}

// inOrder fails unless each of want is among the commands, after the one before.
func inOrder(t *testing.T, fake *engine.Fake, want ...string) {
	t.Helper()
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, w := range want {
		i := strings.Index(cmds[pos:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", w, cmds)
		}
		pos += i + len(w)
	}
}

func TestUserEnrollMovesFilesListsTheUserAndPushesTheUserBranchFirst(t *testing.T) {
	r := &userRepo{hosts: []string{"nas", "pi"}, hostFile: map[string]bool{"nas": true}}
	err, fake, out := runUserEnroll(t, r, userEnrollDoc("alice", aliceAge, aliceKey), "pi")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"git worktree add -q --orphan -b user/alice",
		"sops rotate -i --add-age "+aliceAge+" users/alice/nas.yaml",
		"sops --encrypt --age age1hostrecipient,"+aliceAge+",age1recoveryrecipient",
		"git add enroll users/alice/nas.yaml users/alice/pi.yaml",
		"git commit -q -m 'user-enroll: alice'",
		`sops set --value-stdin hosts/nas.yaml '["users"]["alice"]'`,
		"git rm -q users/alice/nas.yaml",
		"git commit -q -m 'user-enroll: alice on nas'",
		`sops set --value-stdin hosts/pi.yaml '["users"]["alice"]'`,
		"git commit -q -m 'user-enroll: alice on pi'",
		"git push origin user/alice",
		"git push origin host/nas",
		"git push origin host/pi",
	)
	cmds := strings.Join(fake.Commands(), "\n")
	if strings.Contains(cmds, "git rm -q users/alice/pi.yaml") {
		t.Errorf("removed a file pi never had:\n%s", cmds)
	}
	for _, s := range fake.Ran {
		if s.Name == "sops" && s.Args[0] == "set" {
			want := `{"age_recipient":"` + aliceAge + `","signing_key":"` + aliceKey + `"}`
			if s.Stdin != want {
				t.Errorf("the list holds %q, want %q", s.Stdin, want)
			}
		}
	}
}

func TestUserEnrollWithTheSameIdentityAgainDoesNothing(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}, listed: map[string]string{"nas": `{"age_recipient":"` + aliceAge + `","signing_key":"` + aliceKey + `"}`},
		userBranch: true, userFiles: map[string]bool{"nas": true}, enroll: userEnrollDoc("alice", aliceAge, aliceKey)}
	err, fake, out := runUserEnroll(t, r, userEnrollDoc("alice", aliceAge, aliceKey), "nas")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("did not say so:\n%s", out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git worktree") || strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "git push") || strings.HasPrefix(c, "sops") && !strings.HasPrefix(c, "sops -d") {
			t.Errorf("ran %s", c)
		}
	}
}

func TestUserEnrollRefusesAnotherIdentity(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}, userBranch: true, enroll: userEnrollDoc("alice", "age1someoneelse", aliceKey)}
	err, fake, out := runUserEnroll(t, r, userEnrollDoc("alice", aliceAge, aliceKey))
	if err == nil || !strings.Contains(out, "already enrolled with another identity") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git worktree") || strings.HasPrefix(c, "git push") {
			t.Errorf("ran %s", c)
		}
	}
	// A host that lists her with another identity stops it too.
	r = &userRepo{hosts: []string{"nas"}, listed: map[string]string{"nas": `{"age_recipient":"age1someoneelse","signing_key":"` + aliceKey + `"}`}}
	err, _, out = runUserEnroll(t, r, userEnrollDoc("alice", aliceAge, aliceKey), "nas")
	if err == nil || !strings.Contains(out, "lists user 'alice' with another identity") {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestUserEnrollChecksTheEnrollmentFile(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"version":   {"version: 2\nuser: alice\nage_recipient: " + aliceAge + "\nsigning_key: " + aliceKey + "\n", "unsupported enrollment file version"},
		"user":      {userEnrollDoc("bob", aliceAge, aliceKey), "is for user 'bob', not 'alice'"},
		"recipient": {userEnrollDoc("alice", "age1UPPER", aliceKey), "malformed age_recipient"},
		"key":       {userEnrollDoc("alice", aliceAge, "null"), "signing_key"},
	} {
		err, fake, out := runUserEnroll(t, &userRepo{}, tc.doc)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: got %v\n%s", name, err, out)
		}
		for _, c := range fake.Commands() {
			if strings.HasPrefix(c, "git worktree") {
				t.Errorf("%s: ran %s", name, c)
			}
		}
	}
	// A key ssh-keygen does not parse.
	cacheRecovery(t)
	x, _, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "ssh-keygen" {
			return engine.Result{Code: 255}, errors.New("exit 255")
		}
		return (&userRepo{}).respond(s)
	})
	from := filepath.Join(t.TempDir(), "e")
	_ = os.WriteFile(from, []byte(userEnrollDoc("alice", aliceAge, "ssh-ed25519 AAAA-typo")), 0o644)
	err := engine.RunWith(x, SecretsUserEnroll, map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice", "from": from})
	if err == nil || !strings.Contains(out.String(), "not an SSH public key") {
		t.Errorf("got %v\n%s", err, out.String())
	}
}

func TestUserEnrollRefusesAFileEncryptedToAStranger(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}, hostFile: map[string]bool{"nas": true}, badEncrypt: true}
	err, fake, out := runUserEnroll(t, r, userEnrollDoc("alice", aliceAge, aliceKey))
	if err == nil || !strings.Contains(out, "age1stranger") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "git push") {
			t.Errorf("ran %s", c)
		}
	}
}

// A run that stops after the user's commit leaves that commit, says it is not
// pushed, and still removes the worktrees.
func TestUserEnrollRemovesTheWorktreesWhenItStops(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}, hostFile: map[string]bool{"nas": true}}
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "sops" && s.Args[0] == "set" {
			return engine.Result{Code: 1, Stderr: "boom"}, errors.New("exit 1")
		}
		return r.respond(s)
	})
	from := filepath.Join(t.TempDir(), "e")
	_ = os.WriteFile(from, []byte(userEnrollDoc("alice", aliceAge, aliceKey)), 0o644)
	err := engine.RunWith(x, SecretsUserEnroll, map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice", "from": from})
	if err == nil {
		t.Fatalf("no error\n%s", out)
	}
	removed := 0
	for _, c := range fake.Commands() {
		if strings.Contains(c, "worktree remove --force") {
			removed++
		}
		if strings.HasPrefix(c, "git push") {
			t.Errorf("pushed after the failure: %s", c)
		}
	}
	// user/alice's worktree after its commit, host/nas's when the run stopped.
	if removed != 2 {
		t.Errorf("removed %d worktrees, want 2:\n%s", removed, strings.Join(fake.Commands(), "\n"))
	}
	if !strings.Contains(out.String(), "not pushed yet (git -C") || !strings.Contains(out.String(), "user/alice") {
		t.Errorf("did not say user/alice is not pushed:\n%s", out.String())
	}
}

func runUserRetire(t *testing.T, r *userRepo) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(r.respond)
	err := engine.RunWith(x, SecretsUserRetire, map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice"})
	return err, fake, out.String()
}

func TestUserRetireTakesTheUserOffTheHostsThatListThemAndDeletesTheBranch(t *testing.T) {
	r := &userRepo{hosts: []string{"nas", "pi"}, userBranch: true,
		listed: map[string]string{"nas": `{"age_recipient":"` + aliceAge + `","signing_key":"` + aliceKey + `"}`}}
	err, fake, out := runUserRetire(t, r)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		`sops unset hosts/nas.yaml '["users"]["alice"]'`,
		"git commit -q -m 'user-retire: alice on nas'",
		"git push origin host/nas",
		"git branch -D -q user/alice",
	)
	cmds := strings.Join(fake.Commands(), "\n")
	if strings.Contains(cmds, "hosts/pi.yaml '[") || strings.Contains(cmds, "push origin host/pi") {
		t.Errorf("touched pi, which does not list her:\n%s", cmds)
	}
	if !strings.Contains(out, "git push origin --delete user/alice") {
		t.Errorf("did not say how to delete it on the forge:\n%s", out)
	}
}

func TestUserRetireOfAUserNoHostListsStillDeletesTheBranch(t *testing.T) {
	err, fake, out := runUserRetire(t, &userRepo{hosts: []string{"nas"}, userBranch: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := fake.Commands()
	if slices.ContainsFunc(cmds, func(c string) bool { return strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "git push") }) {
		t.Errorf("committed or pushed with nothing listed:\n%s", strings.Join(cmds, "\n"))
	}
	if !slices.Contains(cmds, "git branch -D -q user/alice") {
		t.Errorf("did not delete the branch:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestHostSecretRefusesTheNameUsers(t *testing.T) {
	for _, name := range []string{"users", "users/alice"} {
		err, fake, out := runHostSecret(t, name, `{"sops": {}}`)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), `"users" is where the host's file lists the users`) {
			t.Errorf("%s: got %v\n%s", name, err, out)
		}
		for _, c := range fake.Commands() {
			if strings.HasPrefix(c, "sops set") || strings.HasPrefix(c, "git commit") {
				t.Errorf("%s: ran %s", name, c)
			}
		}
	}
}

// listedAlice is a repo where nas lists alice, who has user/alice with a file for nas.
func listedAlice() *userRepo {
	return &userRepo{hosts: []string{"nas"}, userBranch: true, userFiles: map[string]bool{"nas": true},
		listed: map[string]string{"nas": `{"age_recipient":"` + aliceAge + `","signing_key":"` + aliceKey + `"}`}}
}

func runUserFlow(t *testing.T, f *engine.Flow, r *userRepo, args map[string]string, extra func(engine.Spec)) (error, *engine.Fake, string) {
	t.Helper()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if extra != nil {
			extra(s)
		}
		return r.respond(s)
	})
	a := map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice", "host": "nas"}
	for k, v := range args {
		a[k] = v
	}
	err := engine.RunWith(x, f, a)
	return err, fake, out.String()
}

func TestUserPasswordOfAListedUserWorksOnTheirBranchAndEncryptsToThem(t *testing.T) {
	r := listedAlice()
	r.userFiles["nas"] = false // no file yet: it is created
	err, fake, out := runUserFlow(t, SecretsUserPassword, r, map[string]string{"password": "pw"}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"verify-commit refs/heads/user/alice",
		"sops --encrypt --age age1hostrecipient,"+aliceAge+",age1recoveryrecipient",
		`sops set --value-stdin users/alice/nas.yaml '["password"]'`,
		"git commit -q -m 'user-password: alice@nas'",
	)
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git worktree add") && !strings.HasSuffix(c, " user/alice") {
			t.Errorf("opened a worktree of another branch: %s", c)
		}
	}
}

func TestUserPasswordOfAnUnlistedUserStaysOnTheHostsBranch(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}}
	err, fake, out := runUserFlow(t, SecretsUserPassword, r, map[string]string{"password": "pw"}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, want := range []string{"sops --encrypt --age age1hostrecipient,age1recoveryrecipient ", "worktree add -q", " host/nas"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
	if strings.Contains(cmds, "user/alice") {
		t.Errorf("went to user/alice:\n%s", cmds)
	}
}

func TestAListedUserWithoutABranchIsAnError(t *testing.T) {
	r := listedAlice()
	r.userBranch = false
	for name, f := range map[string]*engine.Flow{"user-password": SecretsUserPassword, "user-key": SecretsUserKey} {
		err, fake, out := runUserFlow(t, f, r, map[string]string{"password": "pw"}, nil)
		if err == nil || !strings.Contains(out, "alice is listed in hosts/nas.yaml, but there is no user/alice") {
			t.Errorf("%s: got %v\n%s", name, err, out)
		}
		for _, c := range fake.Commands() {
			if strings.HasPrefix(c, "git worktree") || strings.HasPrefix(c, "git commit") {
				t.Errorf("%s: ran %s", name, c)
			}
		}
	}
}

func TestUserKeyOfAListedUserWorksOnTheirBranch(t *testing.T) {
	r := listedAlice()
	err, fake, out := runUserFlow(t, SecretsUserKey, r, nil, func(s engine.Spec) {
		if s.Name == "ssh-keygen" {
			for i, a := range s.Args {
				if a == "-f" {
					_ = os.WriteFile(s.Args[i+1], []byte("PRIVATE\n"), 0o600)
					_ = os.WriteFile(s.Args[i+1]+".pub", []byte("ssh-ed25519 AAAA alice@nas\n"), 0o644)
				}
			}
		}
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"cat-file -e refs/heads/user/alice:users/alice/nas.yaml",
		`sops set --value-stdin users/alice/nas.yaml '["ssh_key"]'`,
		"git commit -q -m 'user-key: alice@nas'",
	)
	// Without the file on the user's branch, user-password comes first.
	r.userFiles["nas"] = false
	err, _, out = runUserFlow(t, SecretsUserKey, r, nil, nil)
	if err == nil || !strings.Contains(out, "users/alice/nas.yaml does not exist on user/alice; run user-password first") {
		t.Errorf("got %v\n%s", err, out)
	}
}

func TestUserKeysOfAListedUserCommitsOnTheirBranchAndPushesIt(t *testing.T) {
	err, fake, out := runUserFlow(t, SecretsUserKeys, listedAlice(), map[string]string{"authorized-keys": writeKeys(t, keyA+"\n")}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"verify-commit refs/heads/user/alice",
		`sops set --value-stdin users/alice/nas.yaml '["authorized_keys"]'`,
		"git commit -q -m 'user-keys: alice on nas'",
		"git push origin user/alice",
	)
	if strings.Contains(strings.Join(fake.Commands(), "\n"), "git push origin host/nas") {
		t.Error("pushed the host's branch")
	}
}

// The tip of a listed user's branch is checked against the signers plus the
// user's own key, in a temporary file; two hosts that list her with different
// keys stop the run.
func TestListedUsersTipMayBeSignedByTheirOwnKey(t *testing.T) {
	err, fake, out := runUserFlow(t, SecretsUserKeys, listedAlice(), map[string]string{"authorized-keys": writeKeys(t, keyA+"\n")}, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	found := false
	for _, c := range fake.Commands() {
		if strings.Contains(c, "git -c gpg.ssh.allowedSignersFile=") && strings.HasSuffix(c, "verify-commit refs/heads/user/alice") {
			found = true
		}
	}
	if !found {
		t.Errorf("did not verify with the user's key added:\n%s", strings.Join(fake.Commands(), "\n"))
	}
	r := listedAlice()
	r.hosts = []string{"nas", "pi"}
	r.listed["pi"] = `{"age_recipient":"` + aliceAge + `","signing_key":"ssh-ed25519 AAAAOTHER alice@other"}`
	err, fake, out = runUserFlow(t, SecretsUserKeys, r, map[string]string{"authorized-keys": writeKeys(t, keyA+"\n")}, nil)
	if err == nil || !strings.Contains(out, "list alice with different signing keys") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git worktree") || strings.HasPrefix(c, "git commit") {
			t.Errorf("ran %s", c)
		}
	}
}

func TestHostRekeyRotatesTheFilesOfListedUsersOnTheirBranches(t *testing.T) {
	r := listedAlice()
	cacheRecovery(t)
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if s.Name == "yq" && strings.HasPrefix(args, "(.users") {
			return engine.Result{Stdout: "alice\n"}, nil
		}
		if s.Name == "yq" && s.Stdin == "ENCRYPTED OLD" {
			return engine.Result{Stdout: "age1hostrecipient\n" + aliceAge + "\nage1recoveryrecipient\n"}, nil
		}
		return r.respond(s)
	})
	from := filepath.Join(t.TempDir(), "nas.enroll")
	_ = os.WriteFile(from, []byte("version: 1\nhost: nas\nage_recipient: age1newhost\nsecrets_deploy_pubkey: ssh-ed25519 AAAA x\n"), 0o644)
	// yq answers the enrollment's fields from its text.
	inner := x.Runner.(*engine.Fake).Respond
	x.Runner.(*engine.Fake).Respond = func(s engine.Spec) (engine.Result, error) {
		if s.Name == "yq" && strings.HasPrefix(s.Stdin, "version: 1\nhost: nas") {
			doc := map[string]string{}
			for _, l := range strings.Split(s.Stdin, "\n") {
				if k, v, ok := strings.Cut(l, ": "); ok {
					doc[k] = v
				}
			}
			k := strings.TrimPrefix(strings.Fields(s.Args[0])[0], ".")
			return engine.Result{Stdout: doc[k] + "\n"}, nil
		}
		return inner(s)
	}
	err := engine.RunWith(x, SecretsHostRekey, map[string]string{"repo": t.TempDir(), "recovery": "", "host": "nas", "from": from})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	inOrder(t, fake,
		"sops rotate -i --rm-age age1hostrecipient --add-age age1newhost users/alice/nas.yaml",
		"git commit -q -m 'host-rekey: nas'",
		"git commit -q -m 'host-rekey: nas'",
	)
}
