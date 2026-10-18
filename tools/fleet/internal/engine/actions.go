package engine

import (
	"errors"
	"fmt"
	"strings"
)

// Command is a step that runs one command. Build it with Cmd and the methods
// below; every string is templated from the flow's values.
type Command struct {
	name        string
	args        []string
	dir         string
	env         []string
	stdinFrom   string // an input whose value goes to stdin, never shown
	stdinText   string
	capture     string // keep the trimmed stdout as this value
	readOnly    bool   // changes nothing: runs without asking
	interactive bool
	quiet       bool
	ok          string
	expect      func(Result) error
	prompts     bool   // asks on the terminal by itself; no spinner
	secret      bool   // its output is never shown
	captureEnv  string // keep the output as hidden environment: NAME=output
	stdinNote   string // what the shown command says about its stdin
}

// Cmd starts a command step.
func Cmd(name string, args ...string) *Command { return &Command{name: name, args: args} }

func (c *Command) In(dir string) *Command               { c.dir = dir; return c }
func (c *Command) Env(kv ...string) *Command            { c.env = append(c.env, kv...); return c }
func (c *Command) StdinFrom(input string) *Command      { c.stdinFrom = input; return c }
func (c *Command) Stdin(text string) *Command           { c.stdinText = text; return c }
func (c *Command) Capture(name string) *Command         { c.capture = name; return c }
func (c *Command) ReadOnly() *Command                   { c.readOnly = true; return c }
func (c *Command) Interactive() *Command                { c.interactive = true; return c }
func (c *Command) Quiet() *Command                      { c.quiet = true; return c }
func (c *Command) OK(msg string) *Command               { c.ok = msg; return c }
func (c *Command) Expect(f func(Result) error) *Command { c.expect = f; return c }

// Prompts marks a command that asks on the terminal itself, such as age
// asking for a passphrase, while its output is still captured.
func (c *Command) Prompts() *Command { c.prompts = true; return c }

// CaptureSecret keeps the output as a value, like Capture, but never shows
// it, not even with --verbose.
func (c *Command) CaptureSecret(name string) *Command { c.capture, c.secret = name, true; return c }

// CaptureEnv keeps the output as environment for every later command,
// passed on but never shown: an unlocked identity, for example.
func (c *Command) CaptureEnv(name string) *Command { c.captureEnv, c.secret = name, true; return c }

// StdinNote says what goes to stdin, for the shown command.
func (c *Command) StdinNote(note string) *Command { c.stdinNote = note; return c }

// spec expands the command against the values.
func (c *Command) spec(x *Ctx) (Spec, error) {
	s := Spec{Interactive: c.interactive, Prompts: c.prompts, Hidden: x.Hidden}
	var err error
	if s.Name, err = x.Expand(c.name); err != nil {
		return s, err
	}
	for _, a := range c.args {
		e, err := x.Expand(a)
		if err != nil {
			return s, err
		}
		s.Args = append(s.Args, e)
	}
	if s.Dir, err = x.Expand(c.dir); err != nil {
		return s, err
	}
	for _, kv := range c.env {
		e, err := x.Expand(kv)
		if err != nil {
			return s, err
		}
		s.Env = append(s.Env, e)
	}
	switch {
	case c.stdinFrom != "":
		s.Stdin = x.Values[c.stdinFrom]
	case c.stdinText != "":
		if s.Stdin, err = x.Expand(c.stdinText); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (c *Command) shown(x *Ctx, s Spec) string {
	line := s.String()
	if s.Dir != "" && s.Dir != "." {
		line = "cd " + quote(s.Dir) + " && " + line
	}
	switch {
	case c.stdinNote != "":
		line += "   # " + c.stdinNote + " on stdin"
	case c.stdinFrom != "":
		line += "   # " + c.stdinFrom + " on stdin"
	}
	return line
}

func (c *Command) run(x *Ctx) error {
	_, err := c.exec(x, false)
	return err
}

// exec runs the command; asQuietCheck is used by Check, which reports the
// result itself.
func (c *Command) exec(x *Ctx, asQuietCheck bool) (Result, error) {
	s, err := c.spec(x)
	if err != nil {
		return Result{}, err
	}
	x.UI.Command(c.shown(x, s))
	// A dry run still looks, so that it shows what would really happen; it
	// only leaves out what changes something.
	if x.Options.DryRun && !c.readOnly {
		if c.capture != "" {
			x.Values[c.capture] = "<" + c.capture + ">"
		}
		if !asQuietCheck {
			x.UI.Skip("dry run: not run")
		}
		return Result{}, nil
	}
	if !c.readOnly && !x.Options.Yes {
		if !x.UI.Interactive() {
			return Result{}, errors.New("this step changes things: run on a terminal to be asked, or give --yes")
		}
		ok, err := x.UI.Confirm("Run it?", true)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, ErrStopped
		}
	}
	stop := func() {}
	if !s.Interactive && !s.Prompts {
		stop = x.UI.Spinner(s.Name)
	}
	res, err := x.Runner.Run(x.Context, s)
	stop()
	if !c.quiet || err != nil || x.Options.Verbose {
		max := 20
		if x.Options.Verbose {
			max = 0
		}
		shown := strings.TrimRight(res.Stdout, "\n") + "\n" + res.Stderr
		if c.secret {
			shown = res.Stderr
		}
		x.UI.Output(shown, max)
	}
	if err == nil && c.expect != nil {
		err = c.expect(res)
	}
	if err != nil {
		return res, err
	}
	if c.capture != "" {
		x.Values[c.capture] = strings.TrimSpace(res.Stdout)
	}
	if c.captureEnv != "" {
		x.Hidden = append(x.Hidden, c.captureEnv+"="+strings.TrimSpace(res.Stdout))
	}
	if !asQuietCheck {
		msg := c.ok
		if msg == "" {
			msg = "done"
		}
		if msg, err = x.Expand(msg); err != nil {
			return res, err
		}
		x.UI.OK(msg)
	}
	return res, nil
}

// Seq runs actions in order, under one step.
func Seq(actions ...Action) Action { return seq(actions) }

type seq []Action

func (s seq) run(x *Ctx) error {
	for _, a := range s {
		if err := a.run(x); err != nil {
			return err
		}
	}
	return nil
}

// Probe is one read-only check: what it checks, the command, and how to read
// its output. Expect returns a detail to show on success, or an error.
type Probe struct {
	What   string
	Cmd    *Command
	Expect func(out string) (string, error)
}

// Checks runs read-only probes without asking, reports each as ✓ or ✗, and
// fails the step only after all of them ran.
func Checks(probes ...Probe) Action { return checks(probes) }

type checks []Probe

func (cs checks) run(x *Ctx) error {
	failed := 0
	for _, p := range cs {
		what, err := x.Expand(p.What)
		if err != nil {
			return err
		}
		res, err := p.Cmd.ReadOnly().Quiet().exec(x, true)
		detail := ""
		if err == nil && p.Expect != nil {
			detail, err = p.Expect(res.Stdout)
		}
		if err != nil {
			failed++
			x.UI.Fail(what + ": " + err.Error())
			continue
		}
		if detail != "" {
			what += ": " + detail
		}
		x.UI.OK(what)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(cs))
	}
	return nil
}

// Func is a step written in Go, for logic that is not a command. It reports
// with x.UI itself; returning an error fails the step.
func Func(f func(x *Ctx) error) Action { return fn(f) }

type fn func(*Ctx) error

func (f fn) run(x *Ctx) error { return f(x) }

// Show prints text to copy, such as configuration lines, under the step.
func Show(text string) Action { return show(text) }

type show string

func (s show) run(x *Ctx) error {
	t, err := x.Expand(string(s))
	if err != nil {
		return err
	}
	x.UI.Text(t)
	return nil
}

// Wait asks the person to do something outside fleet, such as a click on a
// web page, and continues when they confirm. With --yes it does not wait.
func Wait(question string) Action { return wait(question) }

type wait string

func (w wait) run(x *Ctx) error {
	q, err := x.Expand(string(w))
	if err != nil {
		return err
	}
	if x.Options.DryRun || x.Options.Yes {
		x.UI.Skip(q + " (not waiting: " + map[bool]string{true: "dry run", false: "--yes"}[x.Options.DryRun] + ")")
		return nil
	}
	if !x.UI.Interactive() {
		return errors.New("this step waits for you: run on a terminal, or give --yes once it is done")
	}
	ok, err := x.UI.Confirm(q, true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStopped
	}
	return nil
}

// Run runs the command from Go code, such as a Func step, the same way a
// step would: shown, asked about if it changes things, its output shown.
func (c *Command) Run(x *Ctx) (Result, error) { return c.exec(x, false) }

// RunQuiet runs a read-only command from Go code and returns its result
// without reporting ✓; for reading state.
func (c *Command) RunQuiet(x *Ctx) (Result, error) { return c.ReadOnly().Quiet().exec(x, true) }

// WaitFor is Wait from Go code.
func WaitFor(x *Ctx, question string) error { return wait(question).run(x) }

// Silently runs a read-only command through the runner without showing it,
// for facts a step decides on (whether it runs at all), not for the steps'
// own work, which is always shown. It returns the trimmed output.
func Silently(x *Ctx, env []string, name string, args ...string) (string, error) {
	res, err := x.Runner.Run(x.Context, Spec{Name: name, Args: args, Env: env, Hidden: x.Hidden})
	return strings.TrimSpace(res.Stdout), err
}
