// Package ui is the one place that decides how fleet looks: a header per
// command, numbered steps with a line saying why, the exact command dimmed,
// its output indented, and a ✓ or ✗. Colour is used sparingly and drops away
// by itself when the output is not a terminal or NO_COLOR is set.
package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// ErrNoTerminal is returned by a prompt when there is no terminal to ask on.
var ErrNoTerminal = errors.New("no terminal to ask on")

// ErrCancelled is returned by a prompt left with Ctrl+C.
var ErrCancelled = errors.New("cancelled")

// Indent lines up everything under a step with the step's title.
const Indent = "      "

// Palette: a few colours, used sparingly. Exported so the help pages (fang)
// and the prompts (huh) use the same ones.
var (
	Accent = lipgloss.Blue
	Dim    = lipgloss.BrightBlack
	Good   = lipgloss.Green
	Bad    = lipgloss.Red
	Warn   = lipgloss.Yellow
)

// UI writes to out and asks questions on in, when in is a terminal.
type UI struct {
	out    io.Writer
	in     *os.File
	tty    bool
	reader *bufio.Reader
	ctx    context.Context // see Bind

	title, step, dim, good, bad, warn lipgloss.Style
}

// New returns a UI on the process's own terminal.
func New() *UI { return NewWith(os.Stdout, os.Stdin) }

// NewWith returns a UI writing to out. Prompts are only possible when in is a
// terminal; tests pass a buffer and nil.
func NewWith(out io.Writer, in *os.File) *UI {
	tty := in != nil && term.IsTerminal(in.Fd())
	u := &UI{out: out, in: in, tty: tty, ctx: context.Background()}
	if in != nil {
		u.reader = bufio.NewReader(in)
	}
	u.title = lipgloss.NewStyle().Bold(true)
	u.step = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	u.dim = lipgloss.NewStyle().Foreground(Dim)
	u.good = lipgloss.NewStyle().Foreground(Good)
	u.bad = lipgloss.NewStyle().Foreground(Bad)
	u.warn = lipgloss.NewStyle().Foreground(Warn)
	return u
}

// Interactive reports whether prompts can be shown.
func (u *UI) Interactive() bool { return u.tty }

func (u *UI) println(s string) { _, _ = lipgloss.Fprintln(u.out, s) }

// Header starts a command: "fleet · <title>".
func (u *UI) Header(title string) {
	u.println(u.title.Render("fleet · " + title))
	u.println("")
}

// Step starts step n of total.
func (u *UI) Step(n, total int, title, why string) {
	label := fmt.Sprintf("[%d/%d]", n, total)
	u.println(u.step.Render(label) + " " + u.title.Render(title))
	if why != "" {
		for _, line := range wrap(why, 72) {
			u.println(Indent + line)
		}
	}
}

// Command shows a command exactly as it will run.
func (u *UI) Command(cmd string) { u.println(Indent + u.dim.Render("$ "+cmd)) }

// Output shows what a command printed, indented and dimmed. Only the last
// max lines are shown when max > 0; the rest are counted.
func (u *UI) Output(text string, max int) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	if max > 0 && len(lines) > max {
		u.println(Indent + u.dim.Render(fmt.Sprintf("… %d earlier lines (--verbose shows them)", len(lines)-max)))
		lines = lines[len(lines)-max:]
	}
	for _, l := range lines {
		u.println(Indent + "  " + u.dim.Render(l))
	}
}

// OK, Fail, Skip and Note end a step or report a check.
func (u *UI) OK(msg string)   { u.println(Indent + u.good.Render("✓ "+msg)) }
func (u *UI) Fail(msg string) { u.println(Indent + u.bad.Render("✗ "+msg)) }
func (u *UI) Skip(msg string) { u.println(Indent + u.dim.Render("– "+msg)) }
func (u *UI) Note(msg string) {
	for _, line := range wrap(msg, 72) {
		u.println(Indent + u.warn.Render(line))
	}
}

// Text prints plain lines under the current step, such as a snippet to copy.
func (u *UI) Text(s string) {
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		u.println(Indent + "  " + l)
	}
}

// Blank separates steps.
func (u *UI) Blank() { u.println("") }

// Confirm asks a yes/no question on one line, the way the steps show it:
// "Run it? [Y/n]".
func (u *UI) Confirm(question string, def bool) (bool, error) {
	if !u.tty {
		return false, ErrNoTerminal
	}
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	_, _ = lipgloss.Fprint(u.out, Indent+question+" "+u.dim.Render(hint)+" ")
	// Read in the background, so that an interrupted run does not wait for
	// Enter. The reader left behind ends with the process.
	type answer struct {
		line string
		err  error
	}
	answers := make(chan answer, 1)
	go func() {
		line, err := u.reader.ReadString('\n')
		answers <- answer{line, err}
	}()
	var line string
	select {
	case a := <-answers:
		if a.err != nil && a.line == "" {
			return false, a.err
		}
		line = a.line
	case <-u.ctx.Done():
		u.println("")
		return false, ErrCancelled
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Input asks for a line of text.
func (u *UI) Input(title, help, def string, validate func(string) error) (string, error) {
	if !u.tty {
		return "", ErrNoTerminal
	}
	value := def
	field := huh.NewInput().Title(title).Description(help).Value(&value)
	if validate != nil {
		field = field.Validate(validate)
	}
	return value, u.run(field)
}

// Secret asks for a value without showing it.
func (u *UI) Secret(title, help string, validate func(string) error) (string, error) {
	if !u.tty {
		return "", ErrNoTerminal
	}
	var value string
	field := huh.NewInput().Title(title).Description(help).Value(&value).EchoMode(huh.EchoModePassword)
	if validate != nil {
		field = field.Validate(validate)
	}
	return value, u.run(field)
}

// Choose asks for one of options.
func (u *UI) Choose(title, help string, options []string) (string, error) {
	if !u.tty {
		return "", ErrNoTerminal
	}
	var value string
	field := huh.NewSelect[string]().Title(title).Description(help).
		Options(huh.NewOptions(options...)...).Value(&value)
	return value, u.run(field)
}

// MenuRow is one choice of a Menu.
type MenuRow struct {
	Section string // shown beside the first row of its section
	Label   string
	Detail  string // shown above the list while the row is highlighted
}

// Menu offers rows in sections as one list, and returns the index of the
// chosen one. The keys are listed under it.
func (u *UI) Menu(title string, rows []MenuRow) (int, error) {
	if !u.tty {
		return 0, ErrNoTerminal
	}
	width := 0
	for _, r := range rows {
		width = max(width, len([]rune(r.Section)))
	}
	options := make([]huh.Option[int], len(rows))
	for i, r := range rows {
		pad := strings.Repeat(" ", width-len([]rune(r.Section)))
		options[i] = huh.NewOption(r.Section+pad+"   "+r.Label, i)
	}
	var chosen int
	field := huh.NewSelect[int]().Title(title).
		DescriptionFunc(func() string { return rows[chosen].Detail }, &chosen).
		Options(options...).Value(&chosen)
	return chosen, u.form(field, true)
}

func (u *UI) run(field huh.Field) error { return u.form(field, false) }

func (u *UI) form(field huh.Field, help bool) error {
	err := huh.NewForm(huh.NewGroup(field)).
		WithTheme(huh.ThemeFunc(theme)).
		WithShowHelp(help).
		WithInput(u.in).
		WithOutput(u.out).
		RunWithContext(u.ctx)
	if errors.Is(err, huh.ErrUserAborted) || u.ctx.Err() != nil {
		return ErrCancelled
	}
	return err
}

// theme is huh's base theme without its focus border, in the palette above.
func theme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	t.Focused.Base = lipgloss.NewStyle().PaddingLeft(len(Indent))
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = lipgloss.NewStyle().Bold(true)
	t.Focused.Description = lipgloss.NewStyle().Foreground(Dim)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(Accent).SetString("› ")
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(Accent)
	t.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(Bad).SetString(" *")
	t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(Bad)
	t.Blurred = t.Focused
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	return t
}

// Spinner shows that a long command is still running, on a terminal only. It
// returns the function that stops it.
func (u *UI) Spinner(label string) func() {
	if !u.tty {
		return func() {}
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Only for commands that take a while: nothing for the first second.
		select {
		case <-done:
			return
		case <-time.After(time.Second):
		}
		start := time.Now()
		for i := 0; ; i++ {
			select {
			case <-done:
				_, _ = fmt.Fprint(u.out, "\r\033[K")
				return
			case <-time.After(100 * time.Millisecond):
				_, _ = lipgloss.Fprint(u.out, "\r"+Indent+u.dim.Render(fmt.Sprintf("%s %s (%ds)", frames[i%len(frames)], label, int(time.Since(start).Seconds()))))
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

// wrap breaks s into lines of at most width runes, on spaces.
func wrap(s string, width int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len([]rune(line))+1+len([]rune(w)) > width {
				lines = append(lines, line)
				line = w
				continue
			}
			line += " " + w
		}
		lines = append(lines, line)
	}
	return lines
}

// Using shows the values a command runs with, once, under its header: what
// was given and what was assumed, so that a flag can change it.
func (u *UI) Using(values []string) {
	// Lines break between values, never inside one.
	line := ""
	for _, v := range values {
		switch {
		case line == "":
			line = v
		case len([]rune(line))+3+len([]rune(v)) > 76:
			u.println("  " + u.dim.Render(line))
			line = v
		default:
			line += " · " + v
		}
	}
	if line != "" {
		u.println("  " + u.dim.Render(line))
	}
	u.println("")
}

// Bind makes the prompts give up, with ErrCancelled, when ctx is done: when
// the run is interrupted while a question waits for an answer.
func (u *UI) Bind(ctx context.Context) { u.ctx = ctx }

// Section is a heading outside the numbered steps, such as the one a run
// that does not finish shows over what it puts back.
func (u *UI) Section(label, title, why string) {
	u.println(u.warn.Render(label) + " " + u.title.Render(title))
	if why != "" {
		for _, line := range wrap(why, 72) {
			u.println(Indent + line)
		}
	}
}
