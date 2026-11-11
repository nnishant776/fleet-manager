package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A run that stops before its end leaves two things for the next one: a
// command line with every answer it was given, as flags, to type or paste;
// and those answers, kept on this machine, which the next run of the same
// command for the same subject offers to use. Only answers are kept, never
// what the run had done: the steps find that out again by looking, as they
// always do. Secret answers, and anything read from standard input, are
// neither printed nor kept.

// runsDir is where unfinished runs' answers are kept:
// $XDG_STATE_HOME/fleet/runs, or ~/.local/state/fleet/runs.
func runsDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "fleet", "runs"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "fleet", "runs"), nil
}

// runMaxAge is how long an unfinished run's answers are offered.
const runMaxAge = 14 * 24 * time.Hour

// SubrunEnv is set for a fleetmgr command that a flow runs: it is part of
// that flow's run, which keeps the answers and prints the line to go on.
const SubrunEnv = "FLEETMGR_SUBRUN"

// Unfinished is what an unfinished run kept.
type Unfinished struct {
	Flow    string            `json:"flow"`    // the command, such as "host adopt"
	Subject string            `json:"subject"` // its first argument's value, such as the host
	When    time.Time         `json:"when"`
	Outcome string            `json:"outcome"` // failed, interrupted or stopped
	Step    int               `json:"step"`
	Of      int               `json:"of"`
	Title   string            `json:"title"` // of the step it stopped at
	Answers map[string]string `json:"answers"`
}

var unsafeKey = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// runFile is the file that keeps a command's unfinished run for a subject.
func runFile(flow []string, subject string) (string, error) {
	dir, err := runsDir()
	if err != nil {
		return "", err
	}
	name := strings.Join(flow, "-")
	if subject != "" {
		name += "-" + unsafeKey.ReplaceAllString(subject, "_")
	}
	return filepath.Join(dir, name+".json"), nil
}

// subject is the value of the flow's first argument, which tells its runs
// apart: the host of fleetmgr host adopt.
func (f *Flow) subject(values map[string]string) string {
	if len(f.Args) == 0 {
		return ""
	}
	v, _, _ := strings.Cut(values[f.Args[0]], "\n")
	return v
}

// answer notes a value the person gave, from a flag or a prompt, for the
// line that goes on and the kept answers. Secrets and standard input are
// left out.
func (c *Ctx) answer(in Input, value string) {
	if in.Secret || value == "-" {
		return
	}
	if c.answers == nil {
		c.answers = map[string]string{}
	}
	c.answers[in.Name] = value
}

// GoOnLine is the command line that runs the flow again with the answers
// given: its arguments first, then the other answers as flags, in the order
// of its inputs.
func GoOnLine(f *Flow, answers map[string]string, opts Options) string {
	words := append([]string{"fleetmgr"}, f.Path...)
	// Arguments go first, in order, as long as each has an answer that reads
	// as one; only the last, when it takes every word that remains, may hold
	// several. The rest are flags.
	positional := map[string]bool{}
	for _, a := range f.Args {
		v, ok := answers[a]
		if !ok || v == "" || strings.HasPrefix(v, "-") || strings.Contains(v, "\n") && a != f.restArg() {
			break
		}
		positional[a] = true
		for _, part := range strings.Split(v, "\n") {
			words = append(words, Quote(part))
		}
	}
	for _, in := range f.Inputs {
		v, ok := answers[in.Name]
		if !ok || positional[in.Name] {
			continue
		}
		switch {
		case in.Bool && v == "true":
			words = append(words, "--"+in.Name)
		case in.Bool:
			words = append(words, "--"+in.Name+"=false")
		case in.Many:
			for _, part := range strings.Split(v, "\n") {
				words = append(words, flagWord(in.Name, part)...)
			}
		default:
			words = append(words, flagWord(in.Name, v)...)
		}
	}
	if opts.Yes {
		words = append(words, "--yes")
	}
	if opts.Verbose {
		words = append(words, "--verbose")
	}
	if opts.DryRun {
		words = append(words, "--dry-run")
	}
	return strings.Join(words, " ")
}

// flagWord is a flag with its value: --name value, or --name=value for a
// value that could be read as a flag of its own.
func flagWord(name, v string) []string {
	if strings.HasPrefix(v, "-") {
		return []string{Quote("--" + name + "=" + v)}
	}
	return []string{"--" + name, Quote(v)}
}

// loadUnfinished reads a kept run; one too old is removed and not offered.
func loadUnfinished(path string, now time.Time) (*Unfinished, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var u Unfinished
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if now.Sub(u.When) > runMaxAge {
		_ = os.Remove(path)
		return nil, os.ErrNotExist
	}
	return &u, nil
}

// saveUnfinished keeps a run's answers, readable by this account alone.
func saveUnfinished(path string, u Unfinished) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ListUnfinished are the kept runs, newest first, for the menu.
func ListUnfinished(now time.Time) []Unfinished {
	dir, err := runsDir()
	if err != nil {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []Unfinished
	for _, p := range paths {
		if u, err := loadUnfinished(p, now); err == nil {
			out = append(out, *u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}

// offerUnfinished takes the answers of an earlier run of the flow for the
// same subject, when there is one, and the person wants them: asked on a
// terminal, or with --resume. Flags given now win over them. --forget drops
// them instead.
func (c *Ctx) offerUnfinished(f *Flow) error {
	subject := f.subject(c.flags)
	path, err := runFile(f.Path, subject)
	if err != nil {
		return nil
	}
	if c.Options.Forget {
		if err := os.Remove(path); err == nil {
			c.UI.Note("Forgot the answers kept from an earlier run.")
		}
		return nil
	}
	u, err := loadUnfinished(path, time.Now())
	if errors.Is(err, os.ErrNotExist) {
		if c.Options.Resume {
			c.UI.Note("No earlier run of this to go on from: running it afresh.")
		}
		return nil
	}
	if err != nil {
		return err
	}
	shown := GoOnLine(f, u.Answers, Options{})
	what := fmt.Sprintf("An earlier run %s at step %d of %d, %s, on %s, with the answers:\n  %s",
		u.Outcome, u.Step, u.Of, u.Title, u.When.Local().Format("2006-01-02 15:04"), shown)
	use := c.Options.Resume
	if !use {
		if !c.UI.Interactive() {
			c.UI.Note(what + "\n--resume takes them, --forget drops them.")
			return nil
		}
		c.UI.Note(what)
		if use, err = c.UI.Confirm("Go on with these answers?", true); err != nil {
			return err
		}
	}
	if !use {
		return nil
	}
	if c.flags == nil {
		c.flags = map[string]string{}
	}
	for k, v := range u.Answers {
		if _, given := c.flags[k]; !given {
			c.flags[k] = v
		}
	}
	c.UI.OK("going on with the answers the run of " + u.When.Local().Format("2006-01-02 15:04") + " kept; flags given now win over them")
	return nil
}

// endRun keeps the answers of a run that stopped before its end and prints
// the line that goes on; a run that finished drops what was kept for it.
func (c *Ctx) endRun(f *Flow, err error) {
	if !c.Resumable || c.total == 0 {
		// Nothing to go on from: a run that stopped before its first step
		// says itself what it needs.
		return
	}
	path, perr := runFile(f.Path, f.subject(c.Values))
	if err == nil {
		if perr == nil {
			_ = os.Remove(path)
		}
		return
	}
	if len(c.answers) == 0 {
		return
	}
	c.UI.Blank()
	c.UI.Text("To go on from here, run:\n  " + GoOnLine(f, c.answers, c.Options))
	if c.Options.DryRun || perr != nil {
		return
	}
	u := Unfinished{Flow: strings.Join(f.Path, " "), Subject: f.subject(c.Values), When: time.Now(),
		Outcome: outcomeOf(err), Answers: c.answers}
	var se *StepError
	if errors.As(err, &se) {
		u.Step, u.Of, u.Title = se.N, c.total, se.Title
	}
	if serr := saveUnfinished(path, u); serr == nil {
		c.UI.Text("These answers are kept too: running it again offers them, --forget drops them.")
	}
}
