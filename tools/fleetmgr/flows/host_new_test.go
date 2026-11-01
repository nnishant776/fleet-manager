package flows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

func accountsFor(t *testing.T, v map[string]string) ([]machineAccount, error) {
	t.Helper()
	x, _, _ := newFakeCtx(func(engine.Spec) (engine.Result, error) { return engine.Result{}, nil })
	x.Values["user"] = "support"
	for k, val := range v {
		x.Values[k] = val
	}
	return newHostAccounts(x)
}

func TestNewHostAccountsTakesTheFlag(t *testing.T) {
	got, err := accountsFor(t, map[string]string{"account": "alice:Alice Liddell:admin\nbob:Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alice" || got[0].Full != "Alice Liddell" || !got[0].Privileged ||
		got[1].Name != "bob" || got[1].Privileged || !got[1].Password || got[1].Keys {
		t.Fatalf("accounts = %+v", got)
	}
}

func TestNewHostAccountsNoPasswordAndKeys(t *testing.T) {
	got, err := accountsFor(t, map[string]string{"account": "alice:Alice:admin\nbob:Bob", "no-password": "bob", "keys": "bob=/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Password || !got[1].Keys || !got[0].Password || got[0].Keys {
		t.Fatalf("accounts = %+v", got)
	}
}

func TestNewHostAccountsRefusals(t *testing.T) {
	for name, v := range map[string]map[string]string{
		"twice":               {"account": "bob:Bob\nbob:Bobby"},
		"the support account": {"account": "support:Support"},
		"no-password unknown": {"account": "bob:Bob", "no-password": "carol"},
		"keys unknown":        {"account": "bob:Bob", "keys": "carol=/x"},
		"none":                {},
	} {
		if got, err := accountsFor(t, v); err == nil {
			t.Errorf("%s: accepted %+v", name, got)
		}
	}
}

func TestNewAccountPattern(t *testing.T) {
	for _, ok := range []string{"alice:Alice Liddell:admin", "bob:Bob", "alice:Alice:admin"} {
		if !newAccountPattern.MatchString(ok) {
			t.Errorf("refused %q", ok)
		}
	}
	for _, bad := range []string{"alice", "alice:", "Alice:x", "a:b:c:d"} {
		if newAccountPattern.MatchString(bad) {
			t.Errorf("took %q", bad)
		}
	}
}

func newHostTargetCtx(t *testing.T, subjects string) (*engine.Ctx, *engine.Fake) {
	t.Helper()
	e := newPinEnv(t, "")
	x, fake, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "nix":
			return engine.Result{Stdout: `{"hostBranches":true,"hosts":"config/hosts","users":null,"shared":[]}`}, nil
		case strings.Contains(args, " ls-files "):
			return engine.Result{Stdout: "config/hosts/desk1/default.nix\n"}, nil
		case strings.Contains(args, " log ") && strings.Contains(args, "--diff-filter=A"):
			return engine.Result{Stdout: subjects}, nil
		}
		return e.respond(s)
	})
	x.Values["flake"], x.Values["host"] = e.flake, "desk1"
	return x, fake
}

func TestCheckNewHostTargetRefusesAHostItDidNotAdd(t *testing.T) {
	x, _ := newHostTargetCtx(t, "feat(hosts): desk1, adopted from a machine\n")
	err := checkNewHostTarget(x)
	if err == nil || !strings.Contains(err.Error(), "did not add it") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckNewHostTargetGoesOnFromItsOwnCommit(t *testing.T) {
	// git log lists newest first: the adding commit is the last line.
	x, _ := newHostTargetCtx(t, "fix(hosts): later change\n"+newHostSubject("desk1")+"\n")
	if err := checkNewHostTarget(x); err != nil {
		t.Fatal(err)
	}
	if x.Value("adopt_in_inventory") != "true" {
		t.Errorf("did not note the host is in the inventory")
	}
}

func TestNewHostRelease(t *testing.T) {
	answer := func(nixEval, defaultVersion string) func(engine.Spec) (engine.Result, error) {
		return func(s engine.Spec) (engine.Result, error) {
			args := strings.Join(s.Args, " ")
			switch {
			case strings.Contains(args, "defaultStateVersion"):
				return engine.Result{Stdout: defaultVersion}, nil
			case strings.Contains(args, "system.nixos.release"):
				if nixEval == "fail" {
					return engine.Result{Code: 1}, errors.New("exited with code 1")
				}
				return engine.Result{Stdout: nixEval}, nil
			}
			return engine.Result{}, nil
		}
	}
	for _, c := range []struct{ name, flag, eval, def, want string }{
		{"the flag wins", "24.11", "26.05", "25.11", "24.11"},
		{"the hosts' release", "", "26.05", "25.11", "26.05"},
		{"the inventory default", "", "fail", "25.11", "25.11"},
		{"an empty answer", "", "", "25.11", "25.11"},
	} {
		x, _, _ := newFakeCtx(answer(c.eval, c.def))
		x.Values["flake"], x.Values["state-version"] = "/inv", c.flag
		got, err := newHostRelease(x)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestSharedHostValue(t *testing.T) {
	for _, c := range []struct {
		name, out string
		fail      bool
		want      string
	}{
		{"all equal", `{"a":"Europe/Berlin","b":"Europe/Berlin"}`, false, "Europe/Berlin"},
		{"different", `{"a":"Europe/Berlin","b":"UTC"}`, false, ""},
		{"an error", "", true, ""},
	} {
		x, _, _ := newFakeCtx(func(engine.Spec) (engine.Result, error) {
			if c.fail {
				return engine.Result{Code: 1}, errors.New("exited with code 1")
			}
			return engine.Result{Stdout: c.out}, nil
		})
		x.Values["flake"] = "/inv"
		got, err := sharedHostValue("conf.host.timezone")(x)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

const (
	enrollKey    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaJ2zd1Tv4i10L7o8kJBLT2upzNnBOJc13FgTh09Azx"
	enrollConfig = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBcOnfigKeyForTheNewHostDesk100000000000"
)

func enrollmentDoc(host, configKey string) string {
	doc := "version: 1\nhost: " + host + "\nuser: support\nage_recipient: age1deskrecipient\nsecrets_deploy_pubkey: " + enrollKey + "\n"
	if configKey != "" {
		doc += "config_deploy_pubkey: " + configKey + "\n"
	}
	return doc
}

func enrollmentCtx(t *testing.T, doc, address string) (*engine.Ctx, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "desk1.enroll")
	if doc != "" {
		if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	x, _, _ := newFakeCtx(func(engine.Spec) (engine.Result, error) { return engine.Result{}, nil })
	x.Values["host"], x.Values["repo"], x.Values["imp_address"] = "desk1", dir, address
	return x, file
}

func TestTakeNewHostEnrollment(t *testing.T) {
	x, file := enrollmentCtx(t, enrollmentDoc("desk1", enrollConfig), "git@github.com:example-org/inventory.git")
	x.Values["from"] = file
	if err := takeNewHostEnrollment(x); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"recipient": "age1deskrecipient", "deploy_key": enrollKey, "config_key": enrollConfig,
		"from": file, "config_url": "git@github.com:example-org/inventory.git",
	} {
		if x.Value(k) != want {
			t.Errorf("%s = %q, want %q", k, x.Value(k), want)
		}
	}
}

func TestTakeNewHostEnrollmentRefusals(t *testing.T) {
	x, file := enrollmentCtx(t, enrollmentDoc("laptop3", enrollConfig), "git@github.com:example-org/inventory.git")
	x.Values["from"] = file
	if err := takeNewHostEnrollment(x); err == nil || !strings.Contains(err.Error(), "not of desk1") {
		t.Errorf("another host's file: %v", err)
	}
	x, file = enrollmentCtx(t, enrollmentDoc("desk1", ""), "git@github.com:example-org/inventory.git")
	x.Values["from"] = file
	if err := takeNewHostEnrollment(x); err == nil || !strings.Contains(err.Error(), "configuration key") {
		t.Errorf("an SSH address and no configuration key: %v", err)
	}
	// Without --from, a file at <repo>/<host>.enroll is taken; none, and not on a terminal, is an error.
	x, _ = enrollmentCtx(t, "", "git@github.com:example-org/inventory.git")
	if err := takeNewHostEnrollment(x); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Errorf("no file: %v", err)
	}
	x, file = enrollmentCtx(t, enrollmentDoc("desk1", enrollConfig), "git@github.com:example-org/inventory.git")
	if err := takeNewHostEnrollment(x); err != nil || x.Value("from") != file {
		t.Errorf("the file beside the repo: %v, from=%q", err, x.Value("from"))
	}
}

func TestStoreNewHostPasswordsSkipsWhatIsDone(t *testing.T) {
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if s.Name == "git" && strings.Contains(args, " show ") && strings.HasSuffix(args, ":users/bob/desk1.yaml") {
			return engine.Result{Stdout: sopsFile("bob", []string{"password"}, "age1deskrecipient")}, nil
		}
		return engine.Result{}, nil
	})
	x.Values["host"], x.Values["repo"], x.Values["user"] = "desk1", "/secrets", "support"
	x.Values["account"], x.Values["no-password"] = "alice:Alice:admin\nbob:Bob", "alice"
	if err := storeNewHostPasswords(x); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if !strings.HasPrefix(c, "git ") {
			t.Errorf("ran %s", c)
		}
	}
	for _, want := range []string{"alice gets no password", "bob has a password stored already"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
