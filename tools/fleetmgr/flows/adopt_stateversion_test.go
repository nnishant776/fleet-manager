package flows

import (
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

// adoptCtx is a context for the steps that talk to the machine, answered by
// respond, which sees each command as one line.
func adoptCtx(respond func(cmd string) (engine.Result, error)) *engine.Ctx {
	x, _, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		return respond(s.Name + " " + strings.Join(s.Args, " "))
	})
	for k, v := range map[string]string{"host": "laptop3", "address": "laptop3", "admin": "alice", "agent": "/tmp/a.sock",
		"admin_prelude": "", "admin_password": "", "flake": "/inv"} {
		x.Values[k] = v
	}
	return x
}

func nothing(string) (engine.Result, error) { return engine.Result{}, nil }

func TestAStateVersionGivenWinsOverEverything(t *testing.T) {
	x := adoptCtx(nothing)
	x.Values["state-version"] = "23.11"
	f := &machineFacts{Host: "laptop3", OrgStateVersion: "25.05", OrgFleetHost: "generic", OrgRevision: strings.Repeat("a", 40)}
	if err := settleStateVersion(x, f, "configuration"); err != nil {
		t.Fatal(err)
	}
	if f.StateVersion != "23.11" || f.StateVersionFrom != "given with --state-version" {
		t.Fatalf("got %q from %q", f.StateVersion, f.StateVersionFrom)
	}
}

func TestTheFleetManagerHostItRunsGivesItsStateVersion(t *testing.T) {
	x := adoptCtx(nothing)
	f := &machineFacts{Host: "laptop3", OrgStateVersion: "25.05", OrgFleetHost: "generic", OrgRevision: strings.Repeat("a", 40)}
	if err := settleStateVersion(x, f, "none"); err != nil {
		t.Fatal(err)
	}
	if f.StateVersion != "25.05" || !strings.Contains(f.StateVersionFrom, "fleet-manager's nixosConfigurations.generic") {
		t.Fatalf("got %q from %q", f.StateVersion, f.StateVersionFrom)
	}
}

func TestAConfigurationNixIsEvaluatedForItsStateVersion(t *testing.T) {
	x := adoptCtx(func(cmd string) (engine.Result, error) {
		if strings.Contains(cmd, "nix-instantiate") {
			return engine.Result{Stdout: `"24.11"` + "\n"}, nil
		}
		return engine.Result{}, nil
	})
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	if err := settleStateVersion(x, f, "configuration"); err != nil {
		t.Fatal(err)
	}
	if f.StateVersion != "24.11" || !strings.Contains(f.StateVersionFrom, "evaluated") {
		t.Fatalf("got %q from %q", f.StateVersion, f.StateVersionFrom)
	}
}

func TestAConfigurationNixThatDoesNotEvaluateIsReadForTheOneValueItSets(t *testing.T) {
	grep := func(out string) *engine.Ctx {
		return adoptCtx(func(cmd string) (engine.Result, error) {
			switch {
			case strings.Contains(cmd, "nix-instantiate"):
				return engine.Result{Code: 1, Stderr: "error: boom"}, errors.New("exited with code 1")
			case strings.Contains(cmd, "grep -hoE"):
				return engine.Result{Stdout: out}, nil
			}
			return engine.Result{}, nil
		})
	}
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	if err := settleStateVersion(grep("system.stateVersion = \"24.11\"\n"), f, "configuration"); err != nil {
		t.Fatal(err)
	}
	if f.StateVersion != "24.11" || !strings.Contains(f.StateVersionFrom, "sets") {
		t.Fatalf("got %q from %q", f.StateVersion, f.StateVersionFrom)
	}
	f = &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	err := settleStateVersion(grep("system.stateVersion = \"24.11\"\nsystem.stateVersion = \"23.05\"\n"), f, "configuration")
	if err == nil || f.StateVersion != "" {
		t.Fatalf("took %q from two different values (%v)", f.StateVersion, err)
	}
}

func TestAFlakeIsEvaluatedForItsStateVersion(t *testing.T) {
	x := adoptCtx(func(cmd string) (engine.Result, error) {
		if strings.Contains(cmd, "/etc/nixos#nixosConfigurations.laptop3") {
			return engine.Result{Stdout: "24.05"}, nil
		}
		return engine.Result{}, nil
	})
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	if err := settleStateVersion(x, f, "flake"); err != nil {
		t.Fatal(err)
	}
	if f.StateVersion != "24.05" {
		t.Fatalf("got %q from %q", f.StateVersion, f.StateVersionFrom)
	}
}

func TestWithNothingExactTheStateVersionIsAskedForAndNotGuessed(t *testing.T) {
	for _, yes := range []bool{false, true} {
		x := adoptCtx(nothing)
		x.Options.Yes = yes
		f := &machineFacts{Host: "laptop3", Hostname: "laptop3", GenFirst: 1, GenFirstRelease: "26.05"}
		err := settleStateVersion(x, f, "none")
		if err == nil || !strings.Contains(err.Error(), "--state-version 26.05") || !strings.Contains(err.Error(), "generation 1") {
			t.Errorf("yes=%v: got %v", yes, err)
		}
		if f.StateVersion != "" {
			t.Errorf("yes=%v: took %q", yes, f.StateVersion)
		}
	}
}

func TestTheInventorysDefaultIsOfferedWhenThereAreNoGenerations(t *testing.T) {
	x := adoptCtx(func(cmd string) (engine.Result, error) {
		if strings.Contains(cmd, "defaultStateVersion") {
			return engine.Result{Stdout: "26.05"}, nil
		}
		return engine.Result{}, nil
	})
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	err := settleStateVersion(x, f, "none")
	if err == nil || !strings.Contains(err.Error(), "the inventory's default is 26.05") || !strings.Contains(err.Error(), "--state-version 26.05") {
		t.Fatalf("got %v", err)
	}
	if f.StateVersion != "" {
		t.Fatalf("took %q", f.StateVersion)
	}
}
