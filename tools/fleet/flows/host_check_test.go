package flows

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// hostAnswers maps a fragment of a remote script to what the host prints.
type hostAnswers map[string]string

func (h hostAnswers) respond(s engine.Spec) (engine.Result, error) {
	script := s.Args[len(s.Args)-1]
	for fragment, out := range h {
		if strings.Contains(script, fragment) {
			return engine.Result{Stdout: out}, nil
		}
	}
	return engine.Result{}, nil
}

func healthyPi() hostAnswers {
	return hostAnswers{
		"id -un":                          "support\nneerajpinew\n",
		"/etc/org/revision":               "eccde35047cbd3a5a09781db27403c7335d83aa6 eccde35047cbd3a5a09781db27403c7335d83aa6\n",
		"status --porcelain":              "0\n",
		"systemctl show -p LoadState":     "loaded\nsuccess\nSun 2026-10-18 18:21:40 IST\nage 50000000000 100000\n",
		"config-auto-update.timer":        "active\nenabled\n",
		"fleet-sync/status":               "",
		"fleet-sync/user-problems":        "",
		"readlink /var/lib/fleet/secrets": "snapshots/a32bf3e3cc506d937af8934906ba14e80762d028\n",
		"find -H /run/secrets":            "hotspot/wls2ccf67579ff5\nwifi_psk\n",
		"is-system-running":               "running\n",
	}
}

func runHostCheck(t *testing.T, answers hostAnswers) (error, *engine.Fake, string) {
	t.Helper()
	var out bytes.Buffer
	fake := &engine.Fake{Respond: answers.respond}
	x := &engine.Ctx{
		Context:  context.Background(),
		UI:       ui.NewWith(&out, nil),
		Runner:   fake,
		Values:   map[string]string{},
		Stdin:    strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostCheck, map[string]string{"host": "neerajpinew", "flake": "/no/such/flake"})
	return err, fake, out.String()
}

func TestHostCheckOnAHealthyHost(t *testing.T) {
	err, fake, out := runHostCheck(t, healthyPi())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"✓ support at neerajpinew: logged in, hostname neerajpinew",
		"✓ running revision: eccde35047cb, the same as its checkout",
		"✓ checkout: no local changes",
		"✓ last sync: succeeded, at Sun 2026-10-18 18:21:40 IST",
		"✓ sync timer: active",
		"✓ problems recorded for everyone: none",
		"✓ secrets snapshot: commit a32bf3e3cc50",
		"✓ users' own secrets: all up to date",
		"✓ decrypted secrets: hotspot/wls2ccf67579ff5, wifi_psk",
		"✓ system state: running, no failed units",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// Every command goes in as support, forwards nothing, and never prompts.
	for _, s := range fake.Ran {
		cmd := s.String()
		for _, want := range []string{"ssh ", "ForwardAgent=no", "BatchMode=yes", "support@neerajpinew"} {
			if !strings.Contains(cmd, want) {
				t.Errorf("%q lacks %q", cmd, want)
			}
		}
	}
}

func TestHostCheckReportsEveryProblem(t *testing.T) {
	answers := healthyPi()
	answers["/etc/org/revision"] = "eccde35047cbd3a5a09781db27403c7335d83aa6 95c22d432af088369612c35b82c6f291c7b322d2\n"
	answers["fleet-sync/status"] = "kind=signature\ncommit=abc\nsince=2026-10-19T04:25:00+05:30\nmessage=The latest configuration change is not signed by a trusted key.\n"
	answers["is-system-running"] = "degraded\nhostapd-x.service\n"
	err, fake, out := runHostCheck(t, answers)
	if err == nil || !strings.Contains(err.Error(), "2 of 5 steps") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, want := range []string{
		"✗ running revision: runs eccde35047cb but its checkout is at 95c22d432af0",
		"✗ problems recorded for everyone: signature since 2026-10-19T04:25:00+05:30",
		"✗ system state: degraded; failed: hostapd-x.service",
		"✓ secrets snapshot",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(fake.Ran) != 10 {
		t.Errorf("ran %d probes, want all 10", len(fake.Ran))
	}
}

func TestHostCheckNamesTheCommitsAndUsersOfASnapshot(t *testing.T) {
	answers := healthyPi()
	answers["readlink /var/lib/fleet/secrets"] = "snapshots/45808d850822096c81400ce04f9226fe4d8ecb358f368368fae0517f57fee5fa\n" +
		"host 8ed78592e8c0437e04fad0d13f1e5d4bd03e4fd4\n" +
		"user alice host-branch\n" +
		"user dave dac85d32ccc25060bc8857e2d722f0a7a06e89d2\n" +
		"user erin kept 2d4edd5074de7b742e9d97d50abd667b8f7fad15\n"
	err, _, out := runHostCheck(t, answers)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "✓ secrets snapshot: commit 8ed78592e8c0; users: alice (host branch), dave (own branch, dac85d32ccc2), erin (kept the file from 2d4edd5074de)"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

func TestHostCheckReportsEachUserWhoseSecretsCouldNotBeUpdated(t *testing.T) {
	answers := healthyPi()
	answers["fleet-sync/user-problems"] = "dave commit abc on user/dave is not signed by the user's key or an allowed signer\nerin has no branch user/erin\n"
	err, _, out := runHostCheck(t, answers)
	if err == nil || !strings.Contains(err.Error(), "1 of 5 steps") {
		t.Fatalf("got %v\n%s", err, out)
	}
	for _, want := range []string{
		"✗ users' own secrets: 2 could not be updated and keep the file they had:",
		"dave: commit abc on user/dave is not signed by the user's key or an allowed signer",
		"erin: has no branch user/erin",
		"✓ secrets snapshot",
		"✓ system state",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestHostCheckOnAHostWithoutSecrets(t *testing.T) {
	answers := healthyPi()
	answers["readlink /var/lib/fleet/secrets"] = "none\n"
	answers["find -H /run/secrets"] = ""
	err, _, out := runHostCheck(t, answers)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ secrets snapshot: none (this host takes no secrets)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestHostCheckStopsWhenTheHostCannotBeReached(t *testing.T) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: func(engine.Spec) (engine.Result, error) {
		return engine.Result{Code: 255, Stderr: "ssh: Could not resolve hostname example: Name or service not known\n"},
			errors.New("exited with code 255")
	}}
	x := &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostCheck, map[string]string{"host": "example", "flake": "/no/such/flake"})
	if err == nil || !strings.Contains(err.Error(), "stopped at step 2") {
		t.Fatalf("got %v\n%s", err, &out)
	}
	for _, want := range []string{
		"✗ support at example: the name example does not resolve to an address: give its IP address with --address",
		"the steps after this one need it, so they were not run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
	if len(fake.Ran) != 1 || strings.Contains(out.String(), "[3/5]") {
		t.Errorf("went on after the host could not be reached: ran %d commands\n%s", len(fake.Ran), &out)
	}
}

func TestHostCheckReportsAStoppedSyncTimer(t *testing.T) {
	for name, timer := range map[string]string{
		"inactive":  "inactive\nenabled\n",
		"disabled":  "active\ndisabled\n",
		"no answer": "",
	} {
		answers := healthyPi()
		answers["config-auto-update.timer"] = timer
		err, _, out := runHostCheck(t, answers)
		if err == nil || !strings.Contains(err.Error(), "1 of 5 steps") {
			t.Fatalf("%s: got %v\n%s", name, err, out)
		}
		if !strings.Contains(out, "✗ sync timer:") || !strings.Contains(out, "no sync will run until it is active and enabled") {
			t.Errorf("%s: output:\n%s", name, out)
		}
	}
}

func TestHostCheckOnAHostWithoutASyncTimer(t *testing.T) {
	answers := healthyPi()
	answers["config-auto-update.timer"] = "none\n"
	err, _, out := runHostCheck(t, answers)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ sync timer: none (no sync on this host)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestHostCheckAsksWhetherTheTimerIsStoppedAfterAnOldSync(t *testing.T) {
	answers := healthyPi()
	// The last exit was 20 days of uptime ago (uptime 30 days, exit at day 10).
	answers["systemctl show -p LoadState"] = "loaded\nsuccess\nSun 2026-10-18 18:21:40 IST\nage 864000000000 2592000.50\n"
	err, _, out := runHostCheck(t, answers)
	if err == nil || !strings.Contains(err.Error(), "1 of 5 steps") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if !strings.Contains(out, "✗ last sync: the last sync ran 20 days ago: is its timer stopped?") {
		t.Errorf("output:\n%s", out)
	}
	// A sync three days old is fine, and so is a host that has not synced since boot.
	for _, age := range []string{"age 1000000000 260000.0", "age 0 5000"} {
		answers["systemctl show -p LoadState"] = "loaded\nsuccess\nSun 2026-10-18 18:21:40 IST\n" + age + "\n"
		if err, _, out := runHostCheck(t, answers); err != nil {
			t.Errorf("%q: %v\n%s", age, err, out)
		}
	}
}
