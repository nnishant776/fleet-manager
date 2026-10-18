// Package engine runs flows. A flow declares its inputs and its steps; the
// engine takes each input from its flag or, on a terminal, from a prompt,
// then runs the steps one by one: heading, why, the exact command, a question
// before anything changes (unless --yes), the command's output, and ✓ or ✗.
// --dry-run shows every step and command without running any.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
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
	Optional bool   // an empty value is fine
}

// Step is one numbered step of a flow.
type Step struct {
	Title string // templated
	Why   string // templated
	Do    Action
	// When, if set, decides whether the step runs; the reason is shown when
	// it does not.
	When func(*Ctx) (bool, string)
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
	Inputs  []Input
	Needs   []string // tools on PATH the flow cannot work without
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

	flags map[string]string // from the command line
	n     int
	total int
}

// Value returns an input's value, or a value a step captured.
func (c *Ctx) Value(name string) string { return c.Values[name] }

// Expand fills {{.name}} placeholders from the values.
func (c *Ctx) Expand(s string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
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
// first that fails.
func Run(c *Ctx, f *Flow) error {
	if c.Values == nil {
		c.Values = map[string]string{}
	}
	if err := checkTools(f.Needs); err != nil {
		return err
	}
	c.UI.Header(f.Title)
	for _, in := range f.Inputs {
		if err := resolve(c, in); err != nil {
			return err
		}
	}
	c.total = len(f.Steps)
	for i, s := range f.Steps {
		c.n = i + 1
		title, err := c.Expand(s.Title)
		if err != nil {
			return err
		}
		why, err := c.Expand(s.Why)
		if err != nil {
			return err
		}
		c.UI.Step(c.n, c.total, title, why)
		if s.When != nil {
			if ok, reason := s.When(c); !ok {
				c.UI.Skip("skipped: " + reason)
				c.UI.Blank()
				continue
			}
		}
		if err := s.Do.run(c); err != nil {
			if errors.Is(err, ErrStopped) {
				c.UI.Skip("stopped here; nothing after this step was run")
				return err
			}
			c.UI.Fail(err.Error())
			return &StepError{N: c.n, Title: title, Err: err}
		}
		c.UI.Blank()
	}
	if c.Options.DryRun {
		c.UI.Note("Dry run: nothing was run.")
	}
	return nil
}

func resolve(c *Ctx, in Input) error {
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
		switch {
		case !c.UI.Interactive() && def != "":
			value = def
		case !c.UI.Interactive() && in.Optional:
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
	c.Values[in.Name] = value
	return nil
}

func ask(c *Ctx, in Input, def string) (string, error) {
	validate := func(v string) error { return check(in, v) }
	switch {
	case in.Secret:
		return c.UI.Secret(in.Prompt, in.Help, validate)
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
	if v == "" {
		if in.Optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", in.Name)
	}
	if in.Pattern != "" && !regexp.MustCompile(in.Pattern).MatchString(v) {
		return fmt.Errorf("%s: %q does not match %s", in.Name, v, in.Pattern)
	}
	return nil
}

// checkTools fails before the first step when a tool the flow needs is not
// on PATH, rather than halfway through.
func checkTools(tools []string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
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

func (e *StepError) Error() string { return fmt.Sprintf("stopped at step %d, %s", e.N, e.Title) }
func (e *StepError) Unwrap() error { return e.Err }
