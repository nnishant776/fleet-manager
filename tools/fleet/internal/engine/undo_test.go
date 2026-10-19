package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func failing(name string) func(Spec) (Result, error) {
	return func(s Spec) (Result, error) {
		if s.Name == name {
			return Result{Code: 1}, errors.New("exited with code 1")
		}
		return Result{}, nil
	}
}

func TestAStopPutsBackNewestFirstAndSaysWhatStays(t *testing.T) {
	f := &Flow{Path: []string{"t"}, Title: "t", Steps: []Step{
		{Title: "one", Do: Func(func(x *Ctx) error {
			x.Undo("a", Cmd("undo", "a"))
			x.Kept("made", "the thing in {{.where}}")
			return nil
		})},
		{Title: "two", Do: Cmd("change", "b").Before(func(x *Ctx) error {
			x.Undo("b", Cmd("undo", "b"))
			x.Undo("a", Cmd("undo", "a", "again")) // a is registered already
			return nil
		})},
		{Title: "three", Do: Cmd("fails")},
		{Title: "four", Do: Cmd("never")},
	}}
	// Without --yes and without a terminal: putting back must not ask.
	x, fake, out := newCtx(Options{}, failing("fails"))
	x.Values["where"] = "/x"
	for i := range f.Steps[:3] {
		if c, ok := f.Steps[i].Do.(*Command); ok {
			c.ReadOnly()
		}
	}
	err := RunWith(x, f, nil)
	var se *StepError
	if !errors.As(err, &se) || se.N != 3 {
		t.Fatalf("got %v\n%s", err, out)
	}
	if got := strings.Join(fake.Commands(), "\n"); got != "change b\nfails\nundo b\nundo a" {
		t.Errorf("ran:\n%s", got)
	}
	for _, want := range []string{"[stop] Put back what this run changed", "– kept: the thing in /x", "goes on from here"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAnInterruptedRunStopsAtOnceAndStillCleansUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaned := false
	f := &Flow{Path: []string{"t"}, Title: "t",
		Cleanup: func(x *Ctx) {
			_, err := Cmd("cleanup").ReadOnly().RunQuiet(x)
			cleaned = err == nil
		},
		Steps: []Step{
			{Title: "one", Do: Func(func(x *Ctx) error { x.Undo("a", Cmd("undo", "a")); return nil })},
			{Title: "two", Do: Cmd("slow").ReadOnly()},
			{Title: "three", Do: Cmd("never").ReadOnly()},
		}}
	x, fake, out := newCtx(Options{}, func(s Spec) (Result, error) {
		if s.Name == "slow" {
			cancel() // Ctrl+C while it runs
			return Result{Code: -1}, errors.New("signal: terminated")
		}
		return Result{}, nil
	})
	x.Context = ctx
	err := RunWith(x, f, nil)
	if !errors.Is(err, ErrInterrupted) || err.Error() != "interrupted at step 2, two" {
		t.Fatalf("got %v\n%s", err, out)
	}
	if got := strings.Join(fake.Commands(), "\n"); got != "slow\nundo a\ncleanup" {
		t.Errorf("ran:\n%s", got)
	}
	if !cleaned || !strings.Contains(out.String(), "interrupted here") || strings.Contains(out.String(), "signal: terminated") {
		t.Errorf("cleaned %v; output:\n%s", cleaned, out)
	}
}

func TestDoneAndSettledDropWhatIsComplete(t *testing.T) {
	f := &Flow{Path: []string{"t"}, Title: "t", Steps: []Step{
		{Title: "one", Do: Func(func(x *Ctx) error {
			x.Undo("a", Cmd("undo", "a"))
			x.Undo("b", Cmd("undo", "b"))
			x.Undo("c", Cmd("undo", "c"))
			x.Done("b")
			return nil
		})},
		{Title: "two", Do: Cmd("fails").ReadOnly()},
	}}
	x, fake, _ := newCtx(Options{}, failing("fails"))
	_ = RunWith(x, f, nil)
	if got := strings.Join(fake.Commands(), " / "); got != "fails / undo c / undo a" {
		t.Errorf("Done: ran %s", got)
	}

	f.Steps[0].Do = Func(func(x *Ctx) error { x.Undo("a", Cmd("undo", "a")); x.Settled(); return nil })
	x, fake, out := newCtx(Options{}, failing("fails"))
	_ = RunWith(x, f, nil)
	if got := strings.Join(fake.Commands(), " / "); got != "fails" || strings.Contains(out.String(), "[stop]") {
		t.Errorf("Settled: ran %s\n%s", got, out)
	}
}

func TestNothingIsPutBackAfterSuccessOrInADryRun(t *testing.T) {
	register := Func(func(x *Ctx) error { x.Undo("a", Cmd("undo", "a")); x.Kept("k", "kept"); return nil })
	ok := &Flow{Path: []string{"t"}, Title: "t", Steps: []Step{{Title: "one", Do: register}}}
	x, fake, out := newCtx(Options{}, nil)
	if err := RunWith(x, ok, nil); err != nil || len(fake.Ran) != 0 || strings.Contains(out.String(), "[stop]") {
		t.Errorf("after success: %v, ran %q\n%s", err, fake.Commands(), out)
	}

	dry := &Flow{Path: []string{"t"}, Title: "t", Steps: []Step{{Title: "one", Do: register}, {Title: "two", Do: Cmd("fails").ReadOnly()}}}
	x, fake, out = newCtx(Options{DryRun: true}, failing("fails"))
	_ = RunWith(x, dry, nil)
	if got := strings.Join(fake.Commands(), " / "); got != "fails" || strings.Contains(out.String(), "[stop]") {
		t.Errorf("dry run: ran %s\n%s", got, out)
	}
}

// What a command started elsewhere is said to run on only after an
// interruption: a command that failed by itself waited for it to end.
func TestRunningNotesShowOnlyAfterAnInterruption(t *testing.T) {
	flow := func(name string) *Flow {
		return &Flow{Path: []string{"t"}, Title: "t", Steps: []Step{{Title: "one",
			Do: Cmd(name).ReadOnly().Before(func(x *Ctx) error { x.Running("sync", "the sync may still be running"); return nil })}}}
	}
	x, _, out := newCtx(Options{}, failing("fails"))
	_ = RunWith(x, flow("fails"), nil)
	if strings.Contains(out.String(), "still running") || strings.Contains(out.String(), "[stop]") {
		t.Errorf("a failure reported the sync running:\n%s", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, _, out = newCtx(Options{}, func(Spec) (Result, error) { cancel(); return Result{Code: -1}, errors.New("signal: terminated") })
	x.Context = ctx
	_ = RunWith(x, flow("slow"), nil)
	if !strings.Contains(out.String(), "– kept: the sync may still be running") {
		t.Errorf("an interruption did not report the sync running:\n%s", out)
	}
}
