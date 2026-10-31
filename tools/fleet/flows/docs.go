package flows

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/book"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// RunbookDir is where the runbooks live, from the top of the checkout.
const RunbookDir = "tools/fleet/runbooks"

// GuideDir is where the guide lives, from the top of the checkout.
const GuideDir = "guide"

// Docs writes the runbooks from the flows, and the guide's generated parts.
var Docs = &engine.Flow{
	Path:  []string{"docs"},
	Title: "Write the runbooks and the guide's generated parts",
	Short: "Write the runbook pages, " + RunbookDir + ", from the flows themselves, and the parts of the guide that follow from its order and headings",
	Long: "Every command's page says what it is for, lists its flags, and walks through\n" +
		"its steps with the commands they run, all taken from the command's definition.\n\n" +
		"The guide, in " + GuideDir + "/, is written by hand in the order its SUMMARY.md gives.\n" +
		"This numbers its chapters, puts links to the pages before and after at the\n" +
		"top and bottom of every page, lists each chapter's sections before the first\n" +
		"of them, writes the contents into the preface, README.md, and writes the\n" +
		"index, index.md, from the glossary, the configuration reference and the\n" +
		"commands.\n\n" +
		"Run it after changing a flow or the guide, and commit the pages with the\n" +
		"change; the tests fail while the runbooks are out of date, and --check fails\n" +
		"while any page is, or a link leads nowhere.",
	Example: "  fleet docs\n  fleet docs --check\n  fleet docs --out /tmp/runbooks --guide /tmp/guide",
	Inputs: []engine.Input{
		{Name: "out", Prompt: "Directory for the pages", Help: "Where the pages go; " + RunbookDir + " in the checkout this is run in by default",
			Default: atTop(RunbookDir)},
		{Name: "guide", Prompt: "Directory of the guide", Help: "The guide, with its SUMMARY.md; " + GuideDir + " in the checkout this is run in by default",
			Default: atTop(GuideDir)},
		{Name: "check", Bool: true, Prompt: "Only check the pages?",
			Help: "Write nothing; fail if a page would change, a page and SUMMARY.md disagree, or a link leads nowhere"},
	},
	Steps: []engine.Step{{
		Title: "Write the pages, or check them",
		Why: "The runbooks, one page per command and an index, README.md, and the guide's generated parts: each chapter's number, " +
			"the links at its top and bottom, the list of its sections, the contents and the index. Pages that would not change are left alone, and pages of " +
			"commands that no longer exist are removed. With --check nothing is written: it fails if a page would change, a page and " +
			"SUMMARY.md disagree, or a link points at a file or a heading that does not exist.",
	}},
}

// Set here rather than above: writeDocs lists every flow, Docs among them.
func init() { Docs.Steps[0].Do = engine.Func(writeDocs) }

// atTop is a default under the top of the checkout this is run in, or none
// outside a checkout.
func atTop(dir string) func(*engine.Ctx) (string, error) {
	return func(x *engine.Ctx) (string, error) {
		top, err := engine.Silently(x, nil, "git", "rev-parse", "--show-toplevel")
		if err != nil || top == "" {
			return "", nil
		}
		return filepath.Join(top, dir), nil
	}
}

// staleRunbooks compares the pages with what is in dir: the pages that are
// new or differ, and the files that no flow writes any more.
func staleRunbooks(dir string, pages map[string]string) (changed, gone []string) {
	for name, text := range pages {
		if old, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(old) != text {
			changed = append(changed, name)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if _, ok := pages[e.Name()]; !ok && strings.HasSuffix(e.Name(), ".md") {
			gone = append(gone, e.Name())
		}
	}
	slices.Sort(changed)
	slices.Sort(gone)
	return changed, gone
}

// indexCommands are the commands, for the guide's index.
func indexCommands() []book.Command {
	var cmds []book.Command
	for _, f := range All() {
		cmds = append(cmds, book.Command{
			Name:    "fleet " + strings.Join(f.Path, " "),
			Runbook: "../" + RunbookDir + "/" + engine.RunbookFile(f),
		})
	}
	return cmds
}

func writeDocs(x *engine.Ctx) error {
	out, err := filepath.Abs(x.Value("out"))
	if err != nil {
		return err
	}
	guide, err := filepath.Abs(x.Value("guide"))
	if err != nil {
		return err
	}
	// Paths are shown from the directory above the guide, the top of the
	// checkout, when they are in it.
	top := filepath.Dir(guide)
	show := func(path string) string {
		if rel, err := filepath.Rel(top, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
		return path
	}
	at := func(path string, line int, what string) string {
		return fmt.Sprintf("%s:%d: %s", show(path), line, what)
	}

	runbooks := engine.Runbooks(All())
	changed, gone := staleRunbooks(out, runbooks)
	b, err := book.Make(guide, indexCommands())
	if err != nil {
		return err
	}
	if len(b.Errors) > 0 {
		for _, p := range b.Errors {
			x.UI.Fail(at(p.File, p.Line, p.What))
		}
		return fmt.Errorf("nothing was written: %d problems in the guide", len(b.Errors))
	}
	files, err := b.Files()
	if err != nil {
		return err
	}
	for name, text := range runbooks {
		files[filepath.Join(out, name)] = text
	}
	warnings := append(slices.Clone(b.Warnings), book.Links(files)...)
	pages := b.Changed()

	if x.Value("check") == "true" {
		var problems []string
		for _, name := range changed {
			path := filepath.Join(out, name)
			if old, err := os.ReadFile(path); err == nil {
				problems = append(problems, at(path, book.FirstChange(string(old), runbooks[name]), "out of date; fleet docs writes it"))
			} else {
				problems = append(problems, at(path, 1, "missing; fleet docs writes it"))
			}
		}
		for _, name := range gone {
			problems = append(problems, at(filepath.Join(out, name), 1, "no command writes it; fleet docs removes it"))
		}
		for _, c := range pages {
			if c.New {
				problems = append(problems, at(c.File, 1, "missing; fleet docs writes it"))
			} else {
				problems = append(problems, at(c.File, c.Line, "out of date; fleet docs rewrites it"))
			}
		}
		for _, p := range warnings {
			problems = append(problems, at(p.File, p.Line, p.What))
		}
		if len(problems) > 0 {
			for _, p := range problems {
				x.UI.Fail(p)
			}
			return fmt.Errorf("%d problems in the runbooks and the guide", len(problems))
		}
		x.UI.OK("the runbooks and the guide are current, and every link leads somewhere")
		return nil
	}

	for _, p := range warnings {
		x.UI.Note(at(p.File, p.Line, p.What))
	}
	if len(changed) == 0 && len(gone) == 0 && len(pages) == 0 {
		x.UI.OK("the runbooks in " + show(out) + " and the guide in " + show(guide) + " are current")
		return nil
	}
	for _, name := range changed {
		x.UI.Text("write  " + show(filepath.Join(out, name)))
	}
	for _, name := range gone {
		x.UI.Text("remove " + show(filepath.Join(out, name)))
	}
	for _, c := range pages {
		x.UI.Text("write  " + show(c.File))
	}
	if ok, err := x.Approve("Write them?"); !ok || err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, name := range changed {
		if err := os.WriteFile(filepath.Join(out, name), []byte(runbooks[name]), 0o644); err != nil {
			return err
		}
	}
	for _, name := range gone {
		if err := os.Remove(filepath.Join(out, name)); err != nil {
			return err
		}
	}
	for _, c := range pages {
		if err := os.WriteFile(c.File, []byte(b.Pages[filepath.Base(c.File)]), 0o644); err != nil {
			return err
		}
	}
	x.UI.OK("written; commit them with the change")
	return nil
}
