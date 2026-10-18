package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Spec is one command, fully expanded.
type Spec struct {
	Name        string
	Args        []string
	Dir         string
	Env         []string // extra KEY=value entries
	Stdin       string
	Interactive bool // attached to the terminal, for commands that ask themselves
	// Prompts: the command asks on the terminal by itself (age for a
	// passphrase) while its output is still captured, so no spinner runs.
	Prompts bool
	// Hidden is extra environment that is passed on but never shown, such as
	// an unlocked identity.
	Hidden []string
}

// String renders the command the way a person would type it.
func (s Spec) String() string {
	var b strings.Builder
	for _, e := range s.Env {
		b.WriteString(quote(e) + " ")
	}
	b.WriteString(quote(s.Name))
	for _, a := range s.Args {
		b.WriteString(" " + quote(a))
	}
	return b.String()
}

// Result is what a command did.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Runner runs commands. The real one runs them; tests use Fake.
type Runner interface {
	Run(ctx context.Context, s Spec) (Result, error)
}

// Exec runs commands for real.
type Exec struct{}

func (Exec) Run(ctx context.Context, s Spec) (Result, error) {
	name := s.Name
	// A flow runs another fleet command as "fleet", which is how it is shown
	// and typed; this binary runs it, whether or not it is on the PATH.
	if name == "fleet" {
		if self, err := os.Executable(); err == nil {
			name = self
		}
	}
	cmd := exec.CommandContext(ctx, name, s.Args...)
	cmd.Dir = s.Dir
	if len(s.Env) > 0 || len(s.Hidden) > 0 {
		cmd.Env = append(append(os.Environ(), s.Env...), s.Hidden...)
	}
	var out, errOut bytes.Buffer
	if s.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	} else {
		cmd.Stdin = strings.NewReader(s.Stdin)
		cmd.Stdout, cmd.Stderr = &out, &errOut
	}
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errOut.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
		err = fmt.Errorf("exited with code %d", res.Code)
	default:
		res.Code = -1
	}
	return res, err
}

// Fake records commands instead of running them, and answers with Respond.
type Fake struct {
	Ran     []Spec
	Respond func(Spec) (Result, error)
}

func (f *Fake) Run(_ context.Context, s Spec) (Result, error) {
	f.Ran = append(f.Ran, s)
	if f.Respond == nil {
		return Result{}, nil
	}
	return f.Respond(s)
}

// Commands lists what the fake ran, one string per command.
func (f *Fake) Commands() []string {
	out := make([]string, len(f.Ran))
	for i, s := range f.Ran {
		out[i] = s.String()
	}
	return out
}

// quote leaves plain words alone and single-quotes the rest.
func quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@,+%^", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
