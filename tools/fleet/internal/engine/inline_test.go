package engine

import (
	"errors"
	"strings"
	"testing"
)

// The steps of another flow run inside the running step: its inputs are its
// own for the time they run, and what else the steps keep stays.
func TestRunStepsScopesTheInputsAndKeepsTheRest(t *testing.T) {
	inner := &Flow{Path: []string{"inner"}, Title: "inner",
		Inputs: []Input{{Name: "user"}, {Name: "secret", Secret: true, Optional: true}},
		Steps: []Step{
			{Title: "work as {{.user}}", Why: "because", Do: Cmd("do", "{{.user}}").StdinFrom("secret")},
			{Title: "skipped", When: func(*Ctx) (bool, string) { return false, "not needed" }, Do: Cmd("never")},
			{Title: "keep", Do: Func(func(x *Ctx) error { x.Values["branch"] = "user/" + x.Value("user"); return nil })},
		}}
	outer := &Flow{Path: []string{"outer"}, Title: "outer", Inputs: []Input{{Name: "user"}},
		Steps: []Step{{Title: "run it", Do: Func(func(x *Ctx) error {
			return x.RunSteps(inner.Inputs, map[string]string{"user": "ann", "secret": "s3cret"}, inner.Steps)
		})}}}
	x, fake, out := newCtx(Options{Yes: true}, nil)
	if err := RunWith(x, outer, map[string]string{"user": "support"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(fake.Commands(), "\n"); got != "do ann" {
		t.Errorf("ran:\n%s", got)
	}
	if fake.Ran[0].Stdin != "s3cret" {
		t.Errorf("stdin is %q", fake.Ran[0].Stdin)
	}
	if x.Value("user") != "support" {
		t.Errorf("user is %q after: the outer flow's was not put back", x.Value("user"))
	}
	if _, ok := x.Values["secret"]; ok {
		t.Errorf("the inner input stayed")
	}
	if x.Value("branch") != "user/ann" {
		t.Errorf("what the steps kept is gone: %q", x.Value("branch"))
	}
	for _, want := range []string{"work as ann", "skipped: not needed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("showed the secret:\n%s", out)
	}
}

func TestRunStepsReturnsTheFailureToTheRunningStep(t *testing.T) {
	boom := errors.New("boom")
	var after bool
	steps := []Step{{Title: "a", Do: Func(func(*Ctx) error { return boom })}, {Title: "b", Do: Func(func(*Ctx) error { after = true; return nil })}}
	x, _, out := newCtx(Options{Yes: true}, nil)
	f := &Flow{Path: []string{"o"}, Title: "o", Steps: []Step{{Title: "s", Do: Func(func(x *Ctx) error { return x.RunSteps(nil, nil, steps) })}}}
	if err := RunWith(x, f, nil); !errors.Is(err, boom) || after {
		t.Errorf("got %v, ran on: %v\n%s", err, after, out)
	}
}
