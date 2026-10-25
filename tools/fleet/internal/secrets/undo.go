package secrets

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// RestoreOnStop registers how to put rel, a path in the git checkout dir
// (both templated), back as it is now, should the run stop before its
// change is complete: with git restore when the file is committed and
// unchanged, by removing it when it does not exist yet, and otherwise from a
// copy kept in memory, so that changes of the person's own survive.
func RestoreOnStop(x *engine.Ctx, dir, rel string) error {
	if x.Options.DryRun {
		return nil
	}
	d, err := x.Expand(dir)
	if err != nil {
		return err
	}
	r, err := x.Expand(rel)
	if err != nil {
		return err
	}
	p := filepath.Join(d, r)
	key := "restore " + p
	st, err := os.Stat(p)
	if os.IsNotExist(err) {
		undo := []engine.Action{engine.Cmd("rm", "-f", "--", p).OK(r + " removed")}
		// And the folders made for it, deepest first, which did not exist.
		var made []string
		for f := filepath.Dir(p); f != filepath.Clean(d) && f != filepath.Dir(f); f = filepath.Dir(f) {
			if _, err := os.Stat(f); err == nil {
				break
			}
			made = append(made, f)
		}
		if len(made) > 0 {
			undo = append(undo, engine.Cmd("rmdir", append([]string{"--"}, made...)...).OK("and the folder made for it"))
		}
		x.Undo(key, engine.Seq(undo...))
		return nil
	}
	if err != nil {
		return err
	}
	_, inHead := engine.Silently(x, nil, "git", "-C", d, "cat-file", "-e", "HEAD:"+r)
	_, changed := engine.Silently(x, nil, "git", "-C", d, "diff", "--quiet", "HEAD", "--", r)
	if inHead == nil && changed == nil {
		x.Undo(key, engine.Cmd("git", "-C", d, "restore", "--source=HEAD", "--staged", "--worktree", "--", r).
			OK(r+" is as it was"))
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	x.Undo(key, engine.Func(func(x *engine.Ctx) error {
		if err := os.WriteFile(p, b, st.Mode().Perm()); err != nil {
			return err
		}
		x.UI.OK(r + " is as it was, from the copy fleet kept")
		return nil
	}))
	return nil
}

// Restoring is RestoreOnStop as a command's Before hook, for rel in the
// secrets checkout.
func Restoring(rel string) func(*engine.Ctx) error {
	return func(x *engine.Ctx) error { return RestoreOnStop(x, "{{.repo}}", rel) }
}

// Unstage registers how to take paths in the checkout dir out of the index
// again, before a commit adds them, should the commit not happen.
func Unstage(x *engine.Ctx, dir string, paths []string) error {
	d, err := x.Expand(dir)
	if err != nil {
		return err
	}
	x.Undo("unstage "+d+" "+strings.Join(paths, " "),
		engine.Cmd("git", append([]string{"-C", d, "reset", "-q", "--"}, paths...)...).OK("nothing is staged"))
	return nil
}

// Committed marks the run's changes in the checkout dir as complete, and
// notes, until Pushed, that the commit is not pushed yet.
func Committed(x *engine.Ctx, dir, message string) {
	x.Settled()
	x.Kept("push "+dir, "the commit “"+message+"” in "+dir+", not pushed yet (git -C "+dir+" push)")
}

// Push pushes the secrets checkout, and drops the note that it is not.
func Push() *engine.Command {
	return repoCmd("git", "push").After(func(x *engine.Ctx) error {
		x.Kept("push {{.repo}}", "")
		return nil
	})
}
