package flows

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

const trialSystem = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-nixos-system-pi-26.05"

// trialFacts answers what a trial switch asks, where the host the test stands
// for says nothing: the configuration's secrets and signers, the system the
// build makes, and a new system that lets support in.
func trialFacts(s engine.Spec, res engine.Result) (engine.Result, bool) {
	args := strings.Join(s.Args, " ")
	last := ""
	if len(s.Args) > 0 {
		last = s.Args[len(s.Args)-1]
	}
	switch {
	case s.Name == "nix" && strings.HasSuffix(args, ".config.conf.secrets.repo.url"):
		res.Stdout = "git@github.com:example-org/secrets.git"
	case s.Name == "nix" && strings.HasSuffix(args, ".config.conf.secrets.repo.ref"):
		res.Stdout = "host/pi"
	case s.Name == "nix" && strings.HasSuffix(args, ".config.conf.secrets.statePath"):
		res.Stdout = "/var/lib/fleet"
	case s.Name == "nix" && strings.Contains(args, ".config.conf.fleet.signing.allowedSignersFile --apply"):
		res.Stdout = "me namespaces=\"git\" ssh-ed25519 AAAA\n"
	case s.Name == "ssh" && strings.Contains(last, "--unit=fleetmgr-trial-build"):
		res.Stdout = "built\nsystem=" + trialSystem + "\n"
	case s.Name == "ssh" && strings.Contains(last, "fleetmgr-trial-rollback.timer && echo confirmed"):
		res.Stdout = "confirmed\n"
	default:
		return res, false
	}
	return res, true
}

// trialHost answers like a host whose trial goes as given: the new system
// lets support in after refusals logins, or has gone back already.
func trialHost(refusals int, wentBack bool) func(engine.Spec) (engine.Result, error) {
	tries := 0
	return func(s engine.Spec) (engine.Result, error) {
		last := s.Args[len(s.Args)-1]
		if s.Name == "ssh" && strings.Contains(last, "echo confirmed") {
			tries++
			switch {
			case wentBack:
				return engine.Result{Stdout: "running=/nix/store/old-system\n", Code: 3}, errors.New("exited with code 3")
			case tries <= refusals:
				return engine.Result{Code: 255, Stderr: "ssh: connect to host pi port 22: Connection refused\n"}, errors.New("exited with code 255")
			}
			return engine.Result{Stdout: "confirmed\n"}, nil
		}
		return engine.Result{}, nil
	}
}

func trialCtx(t *testing.T, respond func(engine.Spec) (engine.Result, error)) (*engine.Ctx, *engine.Fake) {
	t.Helper()
	x, fake, _ := newFakeCtx(respond)
	x.Options.Yes = true
	for k, v := range map[string]string{"host": "pi", "address": "pi", "user": "support", "admin": "admin", "agent": "/tmp/a.sock",
		"admin_prelude": "", "admin_password": "", "new_system": trialSystem, "trial-minutes": "5"} {
		x.Values[k] = v
	}
	old := [2]any{confirmTries, confirmPause}
	confirmTries, confirmPause = 4, time.Millisecond
	t.Cleanup(func() { confirmTries, confirmPause = old[0].(int), old[1].(time.Duration) })
	return x, fake
}

func TestTheNewSystemIsConfirmedOnceSupportGetsIn(t *testing.T) {
	x, fake := trialCtx(t, trialHost(2, false))
	if err := confirmNewSystem(x); err != nil {
		t.Fatal(err)
	}
	logins := ranMatching(fake, "support@pi")
	if len(logins) != 3 {
		t.Fatalf("logged in %d times, want 3", len(logins))
	}
	for _, s := range logins {
		if strings.Contains(s.String(), " -A ") || strings.Contains(s.String(), "admin@") {
			t.Errorf("the confirmation forwarded an agent or went in as admin: %s", s)
		}
	}
}

func TestATrialThatWentBackIsNotConfirmed(t *testing.T) {
	x, fake := trialCtx(t, trialHost(0, true))
	err := confirmNewSystem(x)
	if err == nil || !strings.Contains(err.Error(), "gone back already") {
		t.Fatalf("got %v", err)
	}
	if n := len(ranMatching(fake, "support@pi")); n != 1 {
		t.Errorf("tried %d times after finding the old system back", n)
	}
}

func TestANewSystemSupportCannotReachIsLeftToGoBack(t *testing.T) {
	x, fake := trialCtx(t, trialHost(100, false))
	err := confirmNewSystem(x)
	if err == nil || !strings.Contains(err.Error(), "goes back to the system it ran before by itself") {
		t.Fatalf("got %v", err)
	}
	if n := len(ranMatching(fake, "support@pi")); n != 4 {
		t.Errorf("tried %d times, want 4", n)
	}
	if len(ranMatching(fake, "switch-to-configuration boot")) != 0 {
		t.Error("made an unconfirmed system the boot default")
	}
}

func TestABuildThatGivesNoStorePathStops(t *testing.T) {
	x, _ := trialCtx(t, func(s engine.Spec) (engine.Result, error) {
		return engine.Result{Stdout: "system=/etc/passwd\n"}, nil
	})
	x.Values["switch_ref"], x.Values["switch_env"] = "git+https://example.org/c?rev=abc", ""
	if err := buildNewSystem(x); err == nil || !strings.Contains(err.Error(), "no system's store path") {
		t.Fatalf("got %v", err)
	}
}

func TestADroppedConnectionDuringTheActivationGoesOnToTheConfirmation(t *testing.T) {
	x, _ := trialCtx(t, func(s engine.Spec) (engine.Result, error) {
		if strings.Contains(s.Args[len(s.Args)-1], "trial-switch.sh /nix/store") {
			return engine.Result{Code: 255, Stderr: "Connection to pi closed by remote host.\n"}, errors.New("exited with code 255")
		}
		return engine.Result{}, nil
	})
	if err := tryNewSystem(x); err != nil {
		t.Fatalf("a dropped connection failed the step: %v", err)
	}
}

func TestAFailedActivationStops(t *testing.T) {
	x, _ := trialCtx(t, func(s engine.Spec) (engine.Result, error) {
		if strings.Contains(s.Args[len(s.Args)-1], "trial-switch.sh /nix/store") {
			return engine.Result{Code: 3, Stderr: "the activation failed (code 4): going back now\n"}, errors.New("exited with code 3")
		}
		return engine.Result{}, nil
	})
	if err := tryNewSystem(x); err == nil {
		t.Fatal("went on after a failed activation")
	}
}

// The secrets are fetched with the configuration's own signers, before
// anything is built.
func TestTheSecretsAreFetchedWithTheConfigurationsSigners(t *testing.T) {
	x, fake := trialCtx(t, func(s engine.Spec) (engine.Result, error) {
		if res, ok := trialFacts(s, engine.Result{}); ok {
			return res, nil
		}
		return engine.Result{}, nil
	})
	x.Values["has_tooling"], x.Values["eval_flake"] = "true", "/inv"
	if err := fetchSecretsFirst(x); err != nil {
		t.Fatal(err)
	}
	signers := ranMatching(fake, "allowed_signers.first\"")
	if len(signers) != 1 || signers[0].Stdin != "me namespaces=\"git\" ssh-ed25519 AAAA\n" {
		t.Fatalf("wrote the signers as %+v", signers)
	}
	want := "sudo -n fleetmgr-secrets-sync sync --state /var/lib/fleet --host pi --repo git@github.com:example-org/secrets.git --ref host/pi --signers /var/lib/fleet/allowed_signers.first"
	if got := ranMatching(fake, "fleetmgr-secrets-sync sync"); len(got) != 1 || got[0].Args[len(got[0].Args)-1] != want {
		t.Fatalf("ran %+v", got)
	}
}
