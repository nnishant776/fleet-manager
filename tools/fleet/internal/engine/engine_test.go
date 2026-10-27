package engine

import (
	"github.com/spf13/cobra"

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
		{Title: "unlock", Do: Cmd("age", "-d", "recovery.age").ReadOnly().Prompts().CaptureEnv("SOPS_AGE_KEY", "sops")},
		{Title: "use", Do: Cmd("sops", "updatekeys", "f.yaml")},
		{Title: "commit", Do: Cmd("git", "commit", "-m", "x")},
		{Title: "look", Do: Func(func(x *Ctx) error { _, err := Silently(x, nil, "git", "status"); return err })},
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
	// git runs the repository's hooks: the identity must not reach it.
	for _, s := range fake.Ran[2:] {
		if len(s.Hidden) > 0 {
			t.Errorf("%s got the hidden environment %q", s.Name, s.Hidden)
		}
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

func TestDryRunStillLooks(t *testing.T) {
	x, fake, out := newCtx(Options{DryRun: true}, func(s Spec) (Result, error) {
		return Result{Stdout: "main\n"}, nil
	})
	f := &Flow{Title: "t", Steps: []Step{
		{Title: "look", Do: Cmd("git", "branch", "--show-current").ReadOnly().Capture("branch")},
		{Title: "change", Do: Cmd("git", "push", "origin", "{{.branch}}")},
	}}
	if err := RunWith(x, f, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.Commands(); len(got) != 1 || got[0] != "git branch --show-current" {
		t.Fatalf("a dry run ran %q; it should run only the read-only command", got)
	}
	if !strings.Contains(out.String(), "$ git push origin main") {
		t.Errorf("the change was not shown with the looked-up value:\n%s", out)
	}
}

func TestDefaultsAreUsedAndShownNotAsked(t *testing.T) {
	x, _, out := newCtx(Options{Yes: true}, nil)
	f := &Flow{Title: "t", Inputs: []Input{
		hostInput,
		{Name: "user", Prompt: "Account", Default: func(*Ctx) (string, error) { return "support", nil }},
		{Name: "note", Prompt: "Note", Optional: true},
		{Name: "sync", Prompt: "Sync?", Bool: true},
	}, Steps: []Step{{Do: Cmd("true")}}}
	if err := RunWith(x, f, map[string]string{"host": "pi"}); err != nil {
		t.Fatal(err)
	}
	if x.Value("user") != "support" || x.Value("sync") != "false" {
		t.Fatalf("user %q, sync %q", x.Value("user"), x.Value("sync"))
	}
	if !strings.Contains(out.String(), "host pi · user support · sync false") {
		t.Errorf("the values used are not shown:\n%s", out)
	}
}

func TestShortAndRepeatedFlags(t *testing.T) {
	var got map[string]string
	f := &Flow{Path: []string{"t"}, Title: "t",
		Inputs: []Input{{Name: "host", Short: "H"}, {Name: "disk", Short: "d", Many: true, Pattern: `^\S+$`}},
		Steps: []Step{{Title: "s", Do: Func(func(x *Ctx) error {
			got = map[string]string{"host": x.Value("host"), "disk": x.Value("disk")}
			return nil
		})}},
	}
	run := func(args ...string) error {
		root := &cobra.Command{Use: "fleet", SilenceUsage: true, SilenceErrors: true}
		Mount(root, []*Flow{f}, nil, nil)
		root.SetArgs(args)
		return root.Execute()
	}
	if err := run("t", "-H", "a", "-d", "main=x", "--disk", "y"); err != nil {
		t.Fatal(err)
	}
	if got["host"] != "a" || got["disk"] != "main=x\ny" {
		t.Errorf("got %q", got)
	}
	// The pattern holds for each value, not for them joined.
	if err := run("t", "-H", "a", "-d", "x", "-d", "y z"); err == nil || !strings.Contains(err.Error(), `"y z"`) {
		t.Errorf("a value off the pattern was taken: %v", err)
	}
}

// A flow with Rest takes every word after its other arguments as values of
// its last input, together with the ones given by flag.
func TestRestArgumentTakesTheWordsThatRemain(t *testing.T) {
	var got []string
	f := &Flow{
		Path: []string{"change"}, Title: "t", Args: []string{"name", "host"}, Rest: true,
		Inputs: []Input{
			{Name: "name", Prompt: "name"},
			{Name: "host", Prompt: "host", Many: true, Optional: true},
		},
		Steps: []Step{{Title: "s", Do: Func(func(x *Ctx) error {
			got = []string{x.Value("name"), x.Value("host")}
			return nil
		})}},
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"change", "token", "nas", "pi"}, []string{"token", "nas\npi"}},
		{[]string{"change", "token"}, []string{"token", ""}},
		{[]string{"change", "token", "pi", "--host", "nas"}, []string{"token", "nas\npi"}},
	} {
		root := &cobra.Command{Use: "fleet", SilenceUsage: true, SilenceErrors: true}
		Mount(root, []*Flow{f}, nil, nil)
		root.SetArgs(tc.args)
		got = nil
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
	// Without Rest, a second word is too many.
	f.Rest = false
	root := &cobra.Command{Use: "fleet", SilenceUsage: true, SilenceErrors: true}
	Mount(root, []*Flow{f}, nil, nil)
	root.SetArgs([]string{"change", "token", "nas", "pi"})
	if err := root.Execute(); err == nil {
		t.Error("three words were accepted for two arguments")
	}
}

func TestResolveMapsTheCheckedValue(t *testing.T) {
	x, fake, _ := newCtx(Options{Yes: true}, nil)
	in := hostInput
	in.Resolve = func(x *Ctx, v string) (string, error) {
		if v == "bad" {
			return "", errors.New("no such thing")
		}
		return "/clones/" + v, nil
	}
	f := &Flow{Title: "t", Inputs: []Input{in}, Steps: []Step{{Do: Cmd("ls", "{{.host}}")}}}
	if err := RunWith(x, f, map[string]string{"host": "pi"}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Commands(); len(got) != 1 || got[0] != "ls /clones/pi" {
		t.Fatalf("ran %q", got)
	}
	// The pattern is checked on the value given, before it is resolved.
	x, _, _ = newCtx(Options{Yes: true}, nil)
	if err := RunWith(x, f, map[string]string{"host": "Bad"}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("got %v", err)
	}
	x, _, _ = newCtx(Options{Yes: true}, nil)
	if err := RunWith(x, f, map[string]string{"host": "bad"}); err == nil || !strings.Contains(err.Error(), "host: no such thing") {
		t.Fatalf("got %v", err)
	}
}
