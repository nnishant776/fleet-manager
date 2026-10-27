package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// clock returns a clock that moves by the given steps, one on each call,
// before it answers.
func clock(steps ...time.Duration) func() time.Time {
	t := time.Unix(1000, 0)
	i := 0
	return func() time.Time {
		if i < len(steps) {
			t = t.Add(steps[i])
			i++
		}
		return t
	}
}

func plainLines(s string) []string {
	return strings.Split(strings.TrimRight(ansi.Strip(s), "\n"), "\n")
}

func TestPlainUIPrintsNoDecoration(t *testing.T) {
	var out bytes.Buffer
	u := NewWith(&out, nil)
	u.Header("Check", "dry run")
	u.Using([][2]string{{"host", "pi"}, {"user", "me"}})
	u.Plan([]string{"One", "Two"})
	u.Step(1, 2, "One", "why")
	u.Command("ls")
	u.Output("a\nb", 0)
	u.OK("fine")
	u.StepEnd("ok")
	u.Summary("done", nil)
	want := "fleet · Check\n\n  host pi · user me\n\n[1/2] One\n      why\n      $ ls\n        a\n        b\n      ✓ fine\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestFancyHeaderBar(t *testing.T) {
	var out bytes.Buffer
	u := NewFancy(&out, Look{}, 40, nil)
	u.Header("Check", "dry run")
	lines := plainLines(out.String())
	if lines[0] != " fleet · Check"+strings.Repeat(" ", 40-ansi.StringWidth(" fleet · Check")-len("dry run "))+"dry run " {
		t.Fatalf("bar %q", lines[0])
	}
	if ansi.StringWidth(lines[0]) != 40 {
		t.Fatalf("width %d", ansi.StringWidth(lines[0]))
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatal("no colour in the bar")
	}
}

func TestFancyUsingAligned(t *testing.T) {
	var out bytes.Buffer
	u := NewFancy(&out, Look{}, 60, nil)
	u.Using([][2]string{{"host", "pi"}, {"username", "me"}})
	got := plainLines(out.String())
	if got[0] != "  host      pi" || got[1] != "  username  me" {
		t.Fatalf("got %q", got)
	}
}

func TestFancyStepOutputGutter(t *testing.T) {
	var out bytes.Buffer
	u := NewFancy(&out, Look{}, 60, nil)
	u.Step(2, 6, "Build", "")
	u.Command("make")
	u.Output("a\nb\nc", 2)
	u.Note("careful now")
	got := plainLines(out.String())
	want := []string{" 2/6  Build", Indent + "$ make", Indent + "… 1 earlier lines (--verbose shows them)", Indent + "│ b", Indent + "│ c", Indent + "! careful now"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	var c bytes.Buffer
	uc := NewFancy(&c, Look{Console: true}, 60, nil)
	uc.Output("a\nb\nc", 2)
	if !strings.Contains(c.String(), "... 1 earlier") {
		t.Fatalf("console ellipsis: %q", c.String())
	}
}

func TestFancyGlyphTiers(t *testing.T) {
	var rich, console bytes.Buffer
	r := NewFancy(&rich, Look{}, 50, nil)
	c := NewFancy(&console, Look{Console: true}, 50, nil)
	for _, u := range []*UI{r, c} {
		u.Header("T")
		u.Plan([]string{"A"})
		u.Step(1, 1, "A", "")
		u.OK("yes")
		u.Fail("no")
		u.StepEnd("ok")
		u.Summary("done", nil)
	}
	for _, s := range []string{"✓", "✗", "╭", "╮"} {
		if !strings.Contains(rich.String(), s) {
			t.Errorf("rich lacks %s", s)
		}
	}
	for _, s := range []string{"√", "×", "┌", "┐"} {
		if !strings.Contains(console.String(), s) {
			t.Errorf("console lacks %s", s)
		}
	}
	for _, s := range []string{"✓", "✗", "╭"} {
		if strings.Contains(console.String(), s) {
			t.Errorf("console has %s", s)
		}
	}
}

func TestStepEndTookOnlyFromOneSecond(t *testing.T) {
	var out bytes.Buffer
	// Calls: Header, Step 1, StepEnd 1, Step 2, StepEnd 2.
	u := NewFancy(&out, Look{}, 50, clock(0, 0, 500*time.Millisecond, 0, 2300*time.Millisecond))
	u.Header("T")
	u.Plan([]string{"A", "B"})
	u.Step(1, 2, "A", "")
	u.StepEnd("ok")
	if strings.Contains(out.String(), "took") {
		t.Fatalf("took shown for a fast step: %q", out.String())
	}
	u.Step(2, 2, "B", "")
	u.StepEnd("ok")
	if !strings.Contains(ansi.Strip(out.String()), Indent+"took 2.3s") {
		t.Fatalf("no took line: %q", out.String())
	}
}

func summaryOf(t *testing.T, look Look, tags []string, run func(u *UI), outcome string) (string, []string) {
	t.Helper()
	var out bytes.Buffer
	u := NewFancy(&out, look, 40, clock(time.Second, time.Second, time.Second, time.Second, time.Second, time.Second, time.Second, time.Second))
	u.Header("Check", tags...)
	u.Plan([]string{"First", "Second", "Third"})
	run(u)
	out.Reset()
	u.Summary(outcome, nil)
	return out.String(), plainLines(out.String())
}

func TestSummarySuccessAndColours(t *testing.T) {
	raw, lines := summaryOf(t, Look{}, nil, func(u *UI) {
		for i, name := range []string{"First", "Second", "Third"} {
			u.Step(i+1, 3, name, "")
			u.StepEnd("ok")
		}
	}, "done")
	if !strings.HasPrefix(lines[0], "╭─ Done · Check · ") || !strings.HasSuffix(lines[0], "╮") || len(lines) != 5 {
		t.Fatalf("lines %q", lines)
	}
	if !strings.HasPrefix(raw, "\x1b[32m╭") {
		t.Fatalf("border not green: %q", raw)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 40 {
			t.Fatalf("width %d: %q", ansi.StringWidth(l), l)
		}
	}
	if !strings.Contains(lines[1], "✓ 1  First") {
		t.Fatalf("line %q", lines[1])
	}
}

func TestSummaryFailureSkippedAndDryRun(t *testing.T) {
	raw, lines := summaryOf(t, Look{}, []string{"dry run"}, func(u *UI) {
		u.Step(1, 3, "First", "")
		u.StepEnd("skipped")
		u.Step(2, 3, "Second", "")
		u.StepEnd("failed")
	}, "failed")
	if !strings.HasPrefix(lines[0], "╭─ Failed · Check · ") || !strings.Contains(lines[0], "· dry run") {
		t.Fatalf("top %q", lines[0])
	}
	if !strings.HasPrefix(raw, "\x1b[31m╭") {
		t.Fatalf("border not red: %q", raw)
	}
	if !strings.Contains(lines[1], "skipped") || !strings.Contains(lines[2], "✗ 2  Second") || !strings.Contains(lines[3], "not run") {
		t.Fatalf("lines %q", lines)
	}
}

func TestSummaryStoppedIsYellowAndTitlesAreCut(t *testing.T) {
	var out bytes.Buffer
	u := NewFancy(&out, Look{Console: true}, 24, nil)
	u.Header("A very long title indeed")
	u.Plan([]string{"A very long step title that cannot fit"})
	u.Step(1, 1, "A very long step title that cannot fit", "")
	u.StepEnd("stopped")
	out.Reset()
	u.Summary("stopped", errors.New("x"))
	if !strings.HasPrefix(out.String(), "\x1b[33m┌") {
		t.Fatalf("not yellow: %q", out.String())
	}
	for _, l := range plainLines(out.String()) {
		if ansi.StringWidth(l) != 24 {
			t.Fatalf("width %d: %q", ansi.StringWidth(l), l)
		}
	}
	if !strings.Contains(ansi.Strip(out.String()), "...") {
		t.Fatalf("not cut: %q", out.String())
	}
}
