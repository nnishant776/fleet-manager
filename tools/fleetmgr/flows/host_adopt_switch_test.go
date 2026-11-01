package flows

import (
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

func TestTheListOfWhatStopsComesBetweenTheBuildAndTheTrial(t *testing.T) {
	var titles []string
	for _, s := range machineSwitchSteps {
		titles = append(titles, s.Title)
	}
	all := strings.Join(titles, "\n")
	build := strings.Index(all, "Build {{.host}}'s new system")
	list := strings.Index(all, "List what stops running on {{.host}}")
	try := strings.Index(all, "Try the new system on {{.host}}")
	store := strings.Index(all, "Store the accounts' passwords and keys")
	fits := strings.Index(all, "Check its configuration fits")
	if !(store < fits && fits < build && build < list && list < try) {
		t.Fatalf("out of order:\n%s", all)
	}
	// host enroll has no such step.
	for _, s := range HostEnroll.Steps {
		if strings.HasPrefix(s.Title, "List what stops") {
			t.Fatal("host enroll lists what stops")
		}
	}
}

func TestWhatStopsAtTheSwitchIsListedAndAskedAbout(t *testing.T) {
	x, _, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		return engine.Result{Stdout: "gone=postgresql.service\ngone=evil'; rm -rf /.service\n"}, nil
	})
	for k, v := range map[string]string{"host": "laptop3", "address": "laptop3", "admin": "alice", "agent": "/tmp/a.sock",
		"admin_prelude": "", "admin_password": "", "new_system": trialSystem} {
		x.Values[k] = v
	}
	x.Options.Yes = false
	err := listStoppingServices(x)
	if err == nil {
		t.Fatal("went on with nobody to ask")
	}
	if !strings.Contains(out.String(), "postgresql.service") || strings.Contains(out.String(), "rm -rf") {
		t.Fatalf("listed:\n%s", out)
	}
	x.Options.Yes = true
	if err := listStoppingServices(x); err != nil {
		t.Fatal(err)
	}
}

func TestSupportsKeysAreAskedForOnlyWhenTheBranchHasNone(t *testing.T) {
	has := false
	x, _, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "git" && strings.Contains(args, "cat-file -e"):
			if has {
				return engine.Result{}, nil
			}
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		case s.Name == "git" && strings.Contains(args, "rev-parse --verify"):
			return engine.Result{Stdout: "abc\n"}, nil
		}
		return engine.Result{}, nil
	})
	for k, v := range map[string]string{"host": "laptop3", "user": "support", "repo": "/secrets", "has_tooling": "false"} {
		x.Values[k] = v
	}
	if ok, _ := supportKeysNeeded(x); !ok {
		t.Error("a branch without support's keys does not need them")
	}
	if err := storeSupportKeys(x); err == nil || !strings.Contains(err.Error(), "--support-keys") {
		t.Errorf("got %v", err)
	}
	has = true
	if ok, why := supportKeysNeeded(x); ok {
		t.Errorf("a branch with support's keys needs them again (%s)", why)
	}
}

func TestAnAccountWhosePasswordMovesMustHaveOne(t *testing.T) {
	x, _, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		last := s.Args[len(s.Args)-1]
		switch {
		case s.Name == "nix":
			return engine.Result{Stdout: `{"bob":{"password":true,"keys":false},"support":{"password":false,"keys":true}}`}, nil
		case s.Name == "ssh" && strings.Contains(last, "getent shadow bob"):
			return engine.Result{Stdout: "bob:!:19000::::::\n"}, nil
		}
		return engine.Result{}, nil
	})
	for k, v := range map[string]string{"host": "laptop3", "flake": "/inv", "user": "support", "address": "laptop3", "admin": "alice",
		"agent": "/tmp/a.sock", "admin_prelude": "", "admin_password": ""} {
		x.Values[k] = v
	}
	err := storeAccountSecrets(x)
	if err == nil || !strings.Contains(err.Error(), "bob has none on the machine") {
		t.Fatalf("got %v", err)
	}
}
