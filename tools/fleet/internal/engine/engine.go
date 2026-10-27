// Package engine runs flows. A flow declares its inputs and its steps; the
// engine takes each input from its flag or, on a terminal, from a prompt,
// then runs the steps one by one: heading, why, the exact command, a question
// before anything changes (unless --yes), the command's output, and ✓ or ✗.
// --dry-run shows every step and command, and still runs the read-only ones
// (it looks), but changes nothing.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// Input is one value a flow needs.
type Input struct {
	Name     string // flag name and template key
	Prompt   string
	Help     string
	Default  func(*Ctx) (string, error)
	Choices  func(*Ctx) ([]string, error)
	Pattern  string // the value must match it, when set
	Secret   bool   // hidden prompt; without a terminal it comes from --NAME-stdin
	Twice    bool   // a secret asked for twice, for a new password
	Optional bool   // an empty value is fine
	Bool     bool   // a yes/no switch: --NAME, asked as a yes/no question; "true" or "false"
	Short    string // a one-letter flag as well, such as "H" for -H
	Many     bool   // the flag may be given more than once; the values are joined by newlines
	// Skip, if set, leaves the input out when it does not apply, such as a
	// value that will be read from the host instead.
	Skip func(*Ctx) bool
	// Resolve, if set, maps the value given to the one the flow works with,
	// such as a repository's name to the tool's clone of it. It runs after the
	// value is checked.
	Resolve func(*Ctx, string) (string, error)
}

// Step is one numbered step of a flow.
type Step struct {
	Title string // templated
	Why   string // templated
	Do    Action
	// When, if set, decides whether the step runs; the reason is shown when
	// it does not.
	When func(*Ctx) (bool, string)
	// Required marks a step the ones after it cannot do without, such as
	// reaching the host: when it fails, even a Report flow stops there.
	Required bool
}

// Action is what a step does.
type Action interface {
	run(*Ctx) error
}

// Flow is one command: its place in the command tree, its inputs and steps.
type Flow struct {
	Path    []string // e.g. {"host", "check"}
	Title   string   // shown in the header and the menu
	Short   string   // one line for the help page
	Long    string   // the help page's description
	Example string
	Args    []string // inputs that may also be given as positional arguments, in order
	// Rest lets the last of Args take every word that remains, as the values of
	// its input, which may be given more than once (Many) as well as by flag.
	Rest   bool
	Inputs []Input
	Needs  []string // tools on PATH the flow cannot work without
	// Report marks a read-only flow: every step runs, and the flow fails at
	// the end if any found a problem, instead of stopping at the first.
	Report bool
	// Cleanup runs when the flow ends, however it ends: to stop an agent the
	// flow started, for example.
	Cleanup func(*Ctx)
	Steps   []Step
}

// Options are the switches every flow shares.
type Options struct {
	Yes     bool // run changes without asking
	DryRun  bool // show, run nothing
	Verbose bool // show all output, not only its end
}

// Ctx is a flow while it runs.
type Ctx struct {
	context.Context
	UI      *ui.UI
	Runner  Runner
	Options Options
	Values  map[string]string
	Stdin   io.Reader // for --NAME-stdin inputs
	// LookPath finds a tool on PATH; exec.LookPath unless a test replaces it.
	LookPath func(string) (string, error)
	// Hidden is environment some commands get but nobody sees; see
	// Command.CaptureEnv.
	Hidden []HiddenEnv

	flags map[string]string // from the command line
	n     int
	total int

	undos   []undo // see undo.go
	kept    []note
	undoing bool // putting back: commands run without asking
}

// HiddenEnv is one entry of hidden environment, KEY=value, and the names of
// the commands it goes to.
type HiddenEnv struct {
	Entry string
	To    []string
}

// hiddenFor is the hidden environment the command name gets.
func (c *Ctx) hiddenFor(name string) []string {
	var env []string
	for _, h := range c.Hidden {
		if slices.Contains(h.To, filepath.Base(name)) {
			env = append(env, h.Entry)
		}
	}
	return env
}

// Value returns an input's value, or a value a step captured.
func (c *Ctx) Value(name string) string { return c.Values[name] }

// Say shows text with its {{.name}} placeholders filled in, from Go code.
func (c *Ctx) Say(s string) {
	if t, err := c.Expand(s); err == nil {
		s = t
	}
	c.UI.Text(s)
}

// Expand fills {{.name}} placeholders from the values.
func (c *Ctx) Expand(s string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	// Input names may have hyphens, like their flags (--from-host), which
	// text/template does not accept after a dot.
	for _, m := range hyphenated.FindAllStringSubmatch(s, -1) {
		if _, ok := c.Values[m[1]]; !ok {
			return "", fmt.Errorf("template: no value named %q", m[1])
		}
	}
	s = hyphenated.ReplaceAllString(s, `{{index . "$1"}}`)
	t, err := template.New("").Option("missingkey=error").Parse(s)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, c.Values); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ErrStopped means the person answered no; the flow ends without an error
// of its own.
var ErrStopped = errors.New("stopped")

// Run resolves the inputs, then runs the steps in order and stops at the
// first that fails. A run that stops before its end puts back what it
// changed here (see undo.go); cleanup runs however it ends.
func Run(c *Ctx, f *Flow) (err error) {
	if c.Values == nil {
		c.Values = map[string]string{}
	}
	// The summary comes last, after the cleanup and anything put back, and
	// only for a run that got as far as its steps.
	began := false
	defer func() {
		if began {
			c.UI.Summary(outcomeOf(err), err)
		}
	}()
	if f.Cleanup != nil {
		defer func() {
			// After an interruption the run's context is cancelled, and a
			// cancelled context starts no command, not even a cleanup's.
			c.Context = context.WithoutCancel(c.Context)
			f.Cleanup(c)
		}()
	}
	defer func() {
		if err != nil {
			c.unwind()
		}
	}()
	if c.LookPath == nil {
		c.LookPath = exec.LookPath
	}
	if err := checkTools(c.LookPath, f.Needs); err != nil {
		return err
	}
	var tags []string
	if c.Options.DryRun {
		tags = append(tags, "dry run")
	}
	c.UI.Header(f.Title, tags...)
	var used [][2]string
	for _, in := range f.Inputs {
		if err := resolve(c, in); err != nil {
			return err
		}
		if v, ok := c.Values[in.Name]; ok && !in.Secret && v != "" {
			used = append(used, [2]string{in.Name, v})
		}
	}
	if len(used) > 0 {
		c.UI.Using(used)
	}
	c.total = len(f.Steps)
	began = true
	titles := make([]string, len(f.Steps))
	for i, s := range f.Steps {
		titles[i] = s.Title
		if t, err := c.Expand(s.Title); err == nil {
			titles[i] = t
		}
	}
	c.UI.Plan(titles)
	problems := 0
	for i, s := range f.Steps {
		c.n = i + 1
		title, err := c.Expand(s.Title)
		if err != nil {
			return err
		}
		if c.Context.Err() != nil {
			c.UI.Skip("interrupted; nothing after this was run")
			return &StepError{N: c.n, Title: title, Err: ErrInterrupted}
		}
		why, err := c.Expand(s.Why)
		if err != nil {
			return err
		}
		c.UI.Step(c.n, c.total, title, why)
		if s.When != nil {
			if ok, reason := s.When(c); !ok {
				c.UI.Skip("skipped: " + reason)
				c.UI.StepEnd("skipped")
				c.UI.Blank()
				continue
			}
		}
		if err := s.Do.run(c); err != nil {
			switch {
			case c.Context.Err() != nil:
				// Whatever the step says, it failed because the run was
				// interrupted while it ran.
				c.UI.Skip("interrupted here; nothing after this step was run")
				c.UI.StepEnd("interrupted")
				c.UI.Blank()
				return &StepError{N: c.n, Title: title, Err: ErrInterrupted}
			case errors.Is(err, ErrStopped) || errors.Is(err, ui.ErrCancelled):
				c.UI.Skip("stopped here; nothing after this step was run")
				c.UI.StepEnd("stopped")
				c.UI.Blank()
				return err
			}
			if !errors.As(err, new(alreadyShown)) {
				c.UI.Fail(err.Error())
			}
			c.UI.StepEnd("failed")
			if f.Report && !s.Required {
				problems++
				c.UI.Blank()
				continue
			}
			if f.Report && i+1 < len(f.Steps) {
				c.UI.Skip("the steps after this one need it, so they were not run")
			}
			c.UI.Blank()
			return &StepError{N: c.n, Title: title, Err: err}
		}
		c.UI.StepEnd("ok")
		c.UI.Blank()
	}
	if c.Options.DryRun {
		c.UI.Note("Dry run: nothing was changed.")
	}
	if problems > 0 {
		return fmt.Errorf("%d of %d steps found a problem", problems, c.total)
	}
	return nil
}

// outcomeOf names how a run ended, for the summary.
func outcomeOf(err error) string {
	switch {
	case err == nil:
		return "done"
	case errors.Is(err, ErrInterrupted):
		return "interrupted"
	case errors.Is(err, ErrStopped) || errors.Is(err, ui.ErrCancelled):
		return "stopped"
	}
	return "failed"
}

func resolve(c *Ctx, in Input) error {
	if in.Skip != nil && in.Skip(c) {
		return nil
	}
	value, given := c.flags[in.Name]
	switch {
	case given:
	case in.Secret && c.flags[in.Name+"-stdin"] == "true":
		b, err := io.ReadAll(c.Stdin)
		if err != nil {
			return fmt.Errorf("--%s-stdin: %w", in.Name, err)
		}
		value, given = strings.TrimRight(string(b), "\n"), true
	}
	var def string
	if !given && in.Default != nil {
		d, err := in.Default(c)
		if err != nil {
			return fmt.Errorf("%s: %w", in.Name, err)
		}
		def = d
	}
	if !given {
		// Only what has no good answer is asked for: a value with a default,
		// or one that may be empty, is taken as it is, and shown with the
		// others under the header, so a flag can override it.
		switch {
		case def != "" || in.Optional || in.Bool:
			value = def
			if in.Bool && value == "" {
				value = "false"
			}
		case !c.UI.Interactive():
			flag := "--" + in.Name
			if in.Secret {
				flag += "-stdin"
			}
			return fmt.Errorf("%s is needed: give %s, or run on a terminal to be asked", in.Name, flag)
		default:
			var err error
			value, err = ask(c, in, def)
			if err != nil {
				return err
			}
		}
	}
	if err := check(in, value); err != nil {
		return err
	}
	if in.Resolve != nil {
		v, err := in.Resolve(c, value)
		if err != nil {
			return fmt.Errorf("%s: %w", in.Name, err)
		}
		value = v
	}
	c.Values[in.Name] = value
	return nil
}

func ask(c *Ctx, in Input, def string) (string, error) {
	// Prompts may name earlier values: "Password for {{.user}}".
	var err error
	if in.Prompt, err = c.Expand(in.Prompt); err != nil {
		return "", err
	}
	if in.Help, err = c.Expand(in.Help); err != nil {
		return "", err
	}
	validate := func(v string) error { return check(in, v) }
	switch {
	case in.Bool:
		ok, err := c.UI.Confirm(in.Prompt, def == "true")
		if err != nil {
			return "", err
		}
		return fmt.Sprint(ok), nil
	case in.Secret:
		v, err := c.UI.Secret(in.Prompt, in.Help, validate)
		if err != nil || !in.Twice {
			return v, err
		}
		again, err := c.UI.Secret("Again", "Type it once more, to catch a typo.", nil)
		if err != nil {
			return "", err
		}
		if again != v {
			return "", errors.New("the two entries differ")
		}
		return v, nil
	case in.Choices != nil:
		options, err := in.Choices(c)
		if err != nil {
			return "", fmt.Errorf("%s: %w", in.Name, err)
		}
		if len(options) > 0 {
			return c.UI.Choose(in.Prompt, in.Help, options)
		}
	}
	return c.UI.Input(in.Prompt, in.Help, def, validate)
}

func check(in Input, v string) error {
	if in.Bool && v != "" && v != "true" && v != "false" {
		return fmt.Errorf("%s must be true or false, not %q", in.Name, v)
	}
	if v == "" {
		if in.Optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", in.Name)
	}
	if in.Pattern == "" {
		return nil
	}
	values := []string{v}
	if in.Many {
		values = strings.Split(v, "\n")
	}
	for _, v := range values {
		if !regexp.MustCompile(in.Pattern).MatchString(v) {
			return fmt.Errorf("%s: %q does not match %s", in.Name, v, in.Pattern)
		}
	}
	return nil
}

// checkTools fails before the first step when a tool the flow needs is not
// on PATH, rather than halfway through.
func checkTools(lookPath func(string) (string, error), tools []string) error {
	var missing []string
	for _, t := range tools {
		if _, err := lookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("not on PATH: %s (fleet doctor shows how to get them)", strings.Join(missing, ", "))
	}
	return nil
}

// StepError is a failed step. The step has already shown what went wrong, so
// its message is only a summary of where the flow stopped.
type StepError struct {
	N     int
	Title string
	Err   error
}

func (e *StepError) Error() string {
	if errors.Is(e.Err, ErrInterrupted) {
		return fmt.Sprintf("interrupted at step %d, %s", e.N, e.Title)
	}
	return fmt.Sprintf("stopped at step %d, %s", e.N, e.Title)
}
func (e *StepError) Unwrap() error { return e.Err }

// Approve asks before a change that Go code makes itself, such as writing a
// file, the way a command step asks "Run it?". It returns false without
// asking in a dry run, after saying so.
func (c *Ctx) Approve(question string) (bool, error) {
	if c.Options.DryRun {
		c.UI.Skip("dry run: not done")
		return false, nil
	}
	if c.Options.Yes {
		return true, nil
	}
	if !c.UI.Interactive() {
		return false, errors.New("this step changes things: run on a terminal to be asked, or give --yes")
	}
	ok, err := c.UI.Confirm(question, true)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, ErrStopped
	}
	return true, nil
}

// hyphenated matches {{.a-b}}, a value whose name has a hyphen.
var hyphenated = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)+)\s*\}\}`)
