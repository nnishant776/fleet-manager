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
		"systemctl show -p LoadState":     "loaded\nsuccess\nSun 2026-10-18 18:21:40 IST\n",
		"fleet-sync/status":               "",
		"readlink /var/lib/fleet/secrets": "snapshots/a32bf3e3cc506d937af8934906ba14e80762d028\n",
		"find -H /run/secrets":            "hotspot-wls2ccf67579ff5\n",
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
	err := engine.RunWith(x, HostCheck, map[string]string{"host": "neerajpinew", "flake": "/etc/nixos"})
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
		"✓ problems recorded for everyone: none",
		"✓ secrets snapshot: commit a32bf3e3cc50",
		"✓ decrypted secrets: hotspot-wls2ccf67579ff5",
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
	if err == nil || !strings.Contains(err.Error(), "2 of 4 steps") {
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
	if len(fake.Ran) != 8 {
		t.Errorf("ran %d probes, want all 8", len(fake.Ran))
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
	err := engine.RunWith(x, HostCheck, map[string]string{"host": "example", "flake": "/etc/nixos"})
	if err == nil || !strings.Contains(err.Error(), "stopped at step 1") {
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
	if len(fake.Ran) != 1 || strings.Contains(out.String(), "[2/4]") {
		t.Errorf("went on after the host could not be reached: ran %d commands\n%s", len(fake.Ran), &out)
	}
}
