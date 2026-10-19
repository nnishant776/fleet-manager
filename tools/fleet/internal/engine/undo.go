package engine

import (
	"context"
	"errors"
)

// A run that does not finish puts back what it changed here, and says what
// it leaves.
//
// A step about to change something on this machine registers how to put it
// back with Undo; a step that completes a change, such as a commit, drops
// what is now part of it with Done or Settled. When the flow stops before its
// end, because a step failed, the person answered no, or the run was
// interrupted, the undos run, newest first, without asking: they only put
// back what this run changed. Kept notes say what the run made that stays,
// such as a host's identity or a switch still running on the host, and how
// to go on. A dry run registers nothing, since it changes nothing.

// ErrInterrupted ends a run stopped by Ctrl+C, by closing its terminal, or
// by kill.
var ErrInterrupted = errors.New("interrupted")

type undo struct {
	key string
	do  Action
}

type note struct {
	key, text   string
	interrupted bool // shown only if the run was interrupted
}

// Undo registers how to put back a change about to be made. A key that is
// registered already stays as it is, so that something changed twice is put
// back once, to how it was before the first change.
func (c *Ctx) Undo(key string, a Action) {
	if c.Options.DryRun {
		return
	}
	for _, u := range c.undos {
		if u.key == key {
			return
		}
	}
	c.undos = append(c.undos, undo{key, a})
}

// Done drops one registered undo: that change is complete.
func (c *Ctx) Done(key string) {
	for i, u := range c.undos {
		if u.key == key {
			c.undos = append(c.undos[:i], c.undos[i+1:]...)
			return
		}
	}
}

// Settled drops every registered undo: the changes so far are complete.
func (c *Ctx) Settled() { c.undos = nil }

// Kept sets the note under key, templated, about something this run made
// that a stop leaves as it is, and how to go on; an empty text removes it.
func (c *Ctx) Kept(key, text string) { c.setNote(key, text, false) }

// Running sets the note under key about what a command has started
// elsewhere, such as a unit on a host, that keeps running if the run is
// interrupted while it waits. It is shown only after an interruption: a
// command that ended by itself waited for that to end too.
func (c *Ctx) Running(key, text string) { c.setNote(key, text, true) }

func (c *Ctx) setNote(key, text string, interrupted bool) {
	if c.Options.DryRun {
		return
	}
	for i, n := range c.kept {
		if n.key == key {
			if text == "" {
				c.kept = append(c.kept[:i], c.kept[i+1:]...)
			} else {
				c.kept[i] = note{key, text, interrupted}
			}
			return
		}
	}
	if text != "" {
		c.kept = append(c.kept, note{key, text, interrupted})
	}
}

// unwind runs the undos, newest first, and shows the kept notes. It runs on
// a context of its own, since the run's may be cancelled, and asks nothing.
func (c *Ctx) unwind() {
	interrupted := c.Context.Err() != nil
	var kept []note
	for _, n := range c.kept {
		if interrupted || !n.interrupted {
			kept = append(kept, n)
		}
	}
	if len(c.undos) == 0 && len(kept) == 0 {
		return
	}
	c.Context = context.WithoutCancel(c.Context)
	c.undoing = true
	defer func() { c.undoing = false }()

	title, why := "Put back what this run changed", "The run did not finish. What it changed here is put back; what it made elsewhere stays as it is."
	if len(c.undos) == 0 {
		title, why = "What stays as it is", "The run did not finish, and changed nothing here that needs putting back."
	}
	c.UI.Section("[stop]", title, why)
	failed := false
	for i := len(c.undos) - 1; i >= 0; i-- {
		if err := c.undos[i].do.run(c); err != nil {
			c.UI.Fail(err.Error())
			failed = true
		}
	}
	c.undos = nil
	for _, n := range kept {
		text, err := c.Expand(n.text)
		if err != nil {
			text = n.text
		}
		c.UI.Skip("kept: " + text)
	}
	if failed {
		c.UI.Text("Something could not be put back; the commands above show what it was.")
	}
	c.UI.Text("Steps that are done are skipped, so the same command, run again, goes on from here.")
	c.UI.Blank()
}
