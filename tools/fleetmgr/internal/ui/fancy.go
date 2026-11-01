package ui

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// detectFancy says whether out gets the decorated output: it is a terminal
// and neither NO_COLOR nor FLEET_PLAIN is set (or they are empty).
func detectFancy(out io.Writer, getenv func(string) string) bool {
	f, ok := out.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return false
	}
	return getenv("NO_COLOR") == "" && getenv("FLEET_PLAIN") == ""
}

// NewFancy returns a UI that draws the decorated output into out at the given
// width with the given look, whatever out is, with the colour codes left in;
// for tests. now is its clock (time.Now when nil).
func NewFancy(out io.Writer, look Look, width int, now func() time.Time) *UI {
	u := NewWith(out, nil)
	u.look, u.fancy, u.width, u.raw = look, true, width, true
	if now != nil {
		u.now = now
	}
	return u
}

// termWidth is the width to draw at: the terminal's, at most 100; 80 when
// unknown.
func (u *UI) termWidth() int {
	if u.width > 0 {
		return u.width
	}
	if f, ok := u.out.(*os.File); ok {
		if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
			return min(w, 100)
		}
	}
	return 80
}

// stepRec is what the summary remembers of a step.
type stepRec struct {
	n       int
	title   string
	status  string // "" until the step ends
	started time.Time
	took    time.Duration
}

// Plan tells the UI the titles of all the steps before the first starts, so
// that the summary can name those a run never reached.
func (u *UI) Plan(titles []string) {
	u.steps = u.steps[:0]
	for i, t := range titles {
		u.steps = append(u.steps, stepRec{n: i + 1, title: t})
	}
}

func (u *UI) recordStep(n, total int, title string) {
	for len(u.steps) < total {
		u.steps = append(u.steps, stepRec{n: len(u.steps) + 1})
	}
	if n >= 1 && n <= len(u.steps) {
		u.steps[n-1].title = title
		u.steps[n-1].started = u.now()
		u.steps[n-1].status = ""
		u.cur = n - 1
		u.open = true
	}
}

// StepEnd ends the step that was started last, with status "ok", "failed",
// "skipped", "stopped" or "interrupted". On a terminal a step that took a
// second or more says so.
func (u *UI) StepEnd(status string) {
	if !u.open || u.cur >= len(u.steps) {
		return
	}
	u.open = false
	s := &u.steps[u.cur]
	s.status = status
	s.took = u.now().Sub(s.started)
	if u.fancy && s.took >= time.Second {
		u.println(Indent + u.dim.Render(fmt.Sprintf("took %.1fs", s.took.Seconds())))
	}
}

// Summary closes a run on a terminal with a box listing every step. outcome
// is "done", "failed", "stopped" or "interrupted"; err is the run's error, if
// any. Nothing is printed when the output is plain.
func (u *UI) Summary(outcome string, err error) {
	if !u.fancy {
		return
	}
	g := u.look.Glyphs()
	b := g.Border
	var col color.Color
	var word string
	switch outcome {
	case "done":
		col, word = Good, "Done"
	case "failed":
		col, word = Bad, "Failed"
	case "stopped":
		col, word = Warn, "Stopped"
	default:
		col, word = Warn, "Interrupted"
	}
	edge := lipgloss.NewStyle().Foreground(col)
	w := u.termWidth()
	iw := max(w-2, 0)

	heading := " " + word + " · " + u.title0 + " · " + fmt.Sprintf("%.1fs", u.now().Sub(u.started).Seconds())
	for _, t := range u.tags {
		heading += " · " + t
	}
	heading += " "
	heading = ansi.Truncate(heading, max(w-4, 0), g.Ellipsis)
	fill := max(iw-ansi.StringWidth(heading)-1, 0)
	u.println(edge.Render(b.TopLeft+b.Top) + lipgloss.NewStyle().Bold(true).Foreground(col).Render(heading) + edge.Render(strings.Repeat(b.Top, fill)+b.TopRight))

	for _, s := range u.steps {
		var glyph, right string
		style := u.dim
		switch s.status {
		case "ok":
			glyph, style = g.Ok, u.good
		case "failed":
			glyph, style = g.Fail, u.bad
		case "skipped":
			glyph, right = g.Skip, "skipped"
		case "stopped", "interrupted":
			glyph, style = g.Skip, u.warn
		default:
			glyph, right = g.Skip, "not run"
		}
		if right == "" && s.took >= 100*time.Millisecond {
			right = fmt.Sprintf("%.1fs", s.took.Seconds())
		}
		prefix := " " + style.Render(glyph) + " " + fmt.Sprintf("%d", s.n) + "  "
		room := max(iw-ansi.StringWidth(prefix)-ansi.StringWidth(right)-2, 0)
		title := s.title
		if ansi.StringWidth(title) > room {
			title = ansi.Truncate(title, room, g.Ellipsis)
		}
		gap := max(iw-ansi.StringWidth(prefix)-ansi.StringWidth(title)-ansi.StringWidth(right)-1, 0)
		line := prefix + title + strings.Repeat(" ", gap) + u.dim.Render(right) + " "
		u.println(edge.Render(b.Left) + line + edge.Render(b.Right))
	}
	u.println(edge.Render(b.BottomLeft + strings.Repeat(b.Bottom, iw) + b.BottomRight))
}
