package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// newCtx returns a context with no terminal, a fake runner and captured output.
func newCtx(opts Options, respond func(Spec) (Result, error)) (*Ctx, *Fake, *bytes.Buffer) {
	var out bytes.Buffer
	fake := &Fake{Respond: respond}
	return &Ctx{
		Context: context.Background(),
		UI:      ui.NewWith(&out, nil),
		Runner:  fake,
		Options: opts,
		Values:  map[string]string{},
		Stdin:   strings.NewReader(""),
	}, fake, &out
}

var hostInput = Input{Name: "host", Prompt: "Which host?", Pattern: `^[a-z0-9-]+$`}

func TestFlagsFillInputsAndTemplates(t *testing.T) {
	x, fake, _ := newCtx(Options{Yes: true}, nil)
	f := &Flow{Title: "t", Inputs: []Input{hostInput}, Steps: []Step{
		{Title: "Ping {{.host}}", Do: Cmd("ping", "-c1", "{{.host}}")},
	}}
	if err := RunWith(x, f, map[string]string{"host": "pi"}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Commands(); len(got) != 1 || got[0] != "ping -c1 pi" {
		t.Fatalf("ran %q", got)
	}
}

func TestMissingInputWithoutTerminalNamesTheFlag(t *testing.T) {
	x, fake, _ := newCtx(Options{Yes: true}, nil)
	f := &Flow{Title: "t", Inputs: []Input{hostInput}, Steps: []Step{{Do: Cmd("true")}}}
	err := RunWith(x, f, nil)
	if err == nil || !strings.Contains(err.Error(), "--host") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 0 {
		t.Fatal("ran a command before all inputs were known")
	}
}

func TestDefaultIsUsedWithoutTerminal(t *testing.T) {
	x, _, _ := newCtx(Options{Yes: true}, nil)
	in := hostInput
	in.Default = func(*Ctx) (string, error) { return "generic", nil }
	f := &Flow{Title: "t", Inputs: []Input{in}, Steps: []Step{{Do: Cmd("true")}}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if x.Value("host") != "generic" {
		t.Fatalf("host = %q", x.Value("host"))
	}
}

func TestPatternIsEnforced(t *testing.T) {
	x, _, _ := newCtx(Options{Yes: true}, nil)
	f := &Flow{Title: "t", Inputs: []Input{hostInput}, Steps: []Step{{Do: Cmd("true")}}}
	if err := RunWith(x, f, map[string]string{"host": "Bad Host"}); err == nil {
		t.Fatal("accepted a value that does not match the pattern")
	}
}

func TestDryRunRunsNothingButShowsEverything(t *testing.T) {
	x, fake, out := newCtx(Options{DryRun: true}, nil)
	f := &Flow{Title: "t", Inputs: []Input{hostInput}, Steps: []Step{
		{Title: "one", Do: Cmd("touch", "/tmp/{{.host}}").Capture("made")},
		{Title: "two", Do: Cmd("echo", "{{.made}}")},
	}}
	if err := RunWith(x, f, map[string]string{"host": "pi"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.Ran) != 0 {
		t.Fatalf("dry run ran %q", fake.Commands())
	}
	for _, want := range []string{"$ touch /tmp/pi", "$ echo '<made>'", "dry run: not run", "[2/2] two"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestChangesNeedYesWithoutTerminal(t *testing.T) {
	x, fake, _ := newCtx(Options{}, nil)
	f := &Flow{Title: "t", Steps: []Step{{Do: Cmd("rm", "x")}}}
	if err := RunWith(x, f, nil); err == nil || !strings.Contains(detail(err), "--yes") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 0 {
		t.Fatal("changed something without --yes or a terminal")
	}
}

func TestReadOnlyCommandsRunWithoutYes(t *testing.T) {
	x, fake, _ := newCtx(Options{}, nil)
	f := &Flow{Title: "t", Steps: []Step{{Do: Cmd("git", "status").ReadOnly()}}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.Ran) != 1 {
		t.Fatal("a read-only command did not run")
	}
}

func TestStopsAtTheFirstFailure(t *testing.T) {
	x, fake, out := newCtx(Options{Yes: true}, func(s Spec) (Result, error) {
		if s.Name == "false" {
			return Result{Stderr: "it broke", Code: 1}, errors.New("exited with code 1")
		}
		return Result{}, nil
	})
	f := &Flow{Title: "t", Steps: []Step{
		{Title: "one", Do: Cmd("false")},
		{Title: "two", Do: Cmd("echo", "never")},
	}}
	err := RunWith(x, f, nil)
	var stepErr *StepError
	if !errors.As(err, &stepErr) || stepErr.N != 1 {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 1 {
		t.Fatalf("ran %q after a failure", fake.Commands())
	}
	for _, want := range []string{"it broke", "✗ exited with code 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestSecretComesFromStdinAndIsNeverShown(t *testing.T) {
	x, fake, out := newCtx(Options{Yes: true}, nil)
	x.Stdin = strings.NewReader("hunter2\n")
	f := &Flow{Title: "t", Inputs: []Input{{Name: "value", Prompt: "Value", Secret: true}}, Steps: []Step{
		{Title: "store", Do: Cmd("sops", "set", "--value-stdin", "f.yaml").StdinFrom("value")},
	}}
	if err := RunWith(x, f, map[string]string{"value-stdin": "true"}); err != nil {
		t.Fatal(err)
	}
	if fake.Ran[0].Stdin != "hunter2" {
		t.Fatalf("stdin was %q", fake.Ran[0].Stdin)
	}
	if strings.Contains(out.String(), "hunter2") {
		t.Fatalf("the secret was shown:\n%s", out)
	}
	if !strings.Contains(out.String(), "# value on stdin") {
		t.Errorf("the command does not say where its stdin comes from:\n%s", out)
	}
}

func TestCapturedValuesFeedLaterSteps(t *testing.T) {
	x, fake, _ := newCtx(Options{Yes: true}, func(s Spec) (Result, error) {
		if s.Name == "id" {
			return Result{Stdout: "abc123\n"}, nil
		}
		return Result{}, nil
	})
	f := &Flow{Title: "t", Steps: []Step{
		{Do: Cmd("id").ReadOnly().Capture("rev")},
		{Do: Cmd("checkout", "{{.rev}}")},
	}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.Commands()[1]; got != "checkout abc123" {
		t.Fatalf("second command was %q", got)
	}
}

func TestChecksAllRunAndFailTogether(t *testing.T) {
	x, fake, out := newCtx(Options{}, func(s Spec) (Result, error) {
		return Result{Stdout: s.Args[0]}, nil
	})
	expectYes := func(out string) (string, error) {
		if out != "yes" {
			return "", errors.New("said " + out)
		}
		return "fine", nil
	}
	f := &Flow{Title: "t", Steps: []Step{{Title: "look", Do: Checks(
		Probe{What: "first", Cmd: Cmd("echo", "no"), Expect: expectYes},
		Probe{What: "second", Cmd: Cmd("echo", "yes"), Expect: expectYes},
	)}}}
	err := RunWith(x, f, nil)
	if err == nil || !strings.Contains(detail(err), "1 of 2") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 2 {
		t.Fatal("stopped at the first failed check")
	}
	for _, want := range []string{"✗ first: said no", "✓ second: fine"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestWhenSkipsAStepWithItsReason(t *testing.T) {
	x, fake, out := newCtx(Options{Yes: true}, nil)
	f := &Flow{Title: "t", Steps: []Step{{
		Title: "init",
		Do:    Cmd("init"),
		When:  func(*Ctx) (bool, string) { return false, "the identity exists" },
	}}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.Ran) != 0 || !strings.Contains(out.String(), "skipped: the identity exists") {
		t.Fatalf("ran %q, output:\n%s", fake.Commands(), out)
	}
}

func TestQuoteMatchesWhatAShellWouldNeed(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                 "plain",
		"/var/lib/fleet":        "/var/lib/fleet",
		`["hotspot"]`:           `'["hotspot"]'`,
		"it's":                  `'it'\''s'`,
		"":                      "''",
		"KEY=value with spaces": "'KEY=value with spaces'",
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// detail is what the failed step reported, under the one-line summary.
func detail(err error) string {
	var s *StepError
	if errors.As(err, &s) {
		return s.Err.Error()
	}
	return err.Error()
}

func TestReportRunsEveryStepThenFails(t *testing.T) {
	x, fake, _ := newCtx(Options{}, func(s Spec) (Result, error) {
		if s.Args[0] == "bad" {
			return Result{Code: 1}, errors.New("exited with code 1")
		}
		return Result{}, nil
	})
	f := &Flow{Title: "t", Report: true, Steps: []Step{
		{Title: "one", Do: Checks(Probe{What: "a", Cmd: Cmd("probe", "bad")})},
		{Title: "two", Do: Checks(Probe{What: "b", Cmd: Cmd("probe", "good")})},
	}}
	err := RunWith(x, f, nil)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 steps") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 2 {
		t.Fatalf("a report stopped early: ran %q", fake.Commands())
	}
}

func TestMissingToolsStopBeforeTheFirstStep(t *testing.T) {
	x, fake, _ := newCtx(Options{Yes: true}, nil)
	x.LookPath = func(t string) (string, error) {
		if t == "sops" {
			return "", errors.New("not found")
		}
		return "/bin/" + t, nil
	}
	f := &Flow{Title: "t", Needs: []string{"git", "sops"}, Steps: []Step{{Do: Cmd("git", "status")}}}
	err := RunWith(x, f, nil)
	if err == nil || !strings.Contains(err.Error(), "not on PATH: sops") {
		t.Fatalf("got %v", err)
	}
	if len(fake.Ran) != 0 {
		t.Fatal("ran a step although a tool was missing")
	}
}

func TestSecretOutputAndHiddenEnvAreNeverShown(t *testing.T) {
	x, fake, out := newCtx(Options{Yes: true, Verbose: true}, func(s Spec) (Result, error) {
		if s.Name == "age" {
			return Result{Stdout: "AGE-SECRET-KEY-1SECRET\n"}, nil
		}
		return Result{Stdout: "ok"}, nil
	})
	f := &Flow{Title: "t", Steps: []Step{
		{Title: "unlock", Do: Cmd("age", "-d", "recovery.age").ReadOnly().Prompts().CaptureEnv("SOPS_AGE_KEY")},
		{Title: "use", Do: Cmd("sops", "updatekeys", "f.yaml")},
	}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "SECRET") {
		t.Fatalf("the identity was shown:\n%s", out)
	}
	if got := fake.Ran[1].Hidden; len(got) != 1 || got[0] != "SOPS_AGE_KEY=AGE-SECRET-KEY-1SECRET" {
		t.Fatalf("the later command got hidden env %q", got)
	}
	if strings.Contains(fake.Ran[1].String(), "SOPS_AGE_KEY") {
		t.Fatal("the hidden environment is part of the shown command")
	}
}

func TestTemplatesTakeHyphenatedNames(t *testing.T) {
	x, _, _ := newCtx(Options{}, nil)
	x.Values["from-host"] = "/var/lib/x"
	x.Values["host"] = "pi"
	got, err := x.Expand("cat {{.from-host}} on {{.host}}")
	if err != nil || got != "cat /var/lib/x on pi" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := x.Expand("{{.missing-one}}"); err == nil {
		t.Fatal("a missing hyphenated value expanded without an error")
	}
}
