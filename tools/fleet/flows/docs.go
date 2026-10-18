package flows

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// RunbookDir is where the runbooks live, from the top of the checkout.
const RunbookDir = "tools/fleet/runbooks"

// Docs writes the runbooks from the flows.
var Docs = &engine.Flow{
	Path:  []string{"docs"},
	Title: "Write the runbooks",
	Short: "Write the runbook pages, " + RunbookDir + ", from the flows themselves",
	Long: "Every command's page says what it is for, lists its flags, and walks through\n" +
		"its steps with the commands they run, all taken from the command's definition.\n" +
		"Run it after changing a flow, and commit the pages with the change; the tests\n" +
		"fail while they are out of date.",
	Example: "  fleet docs\n  fleet docs --out /tmp/runbooks",
	Inputs: []engine.Input{
		{Name: "out", Prompt: "Directory for the pages", Help: "Where the pages go; " + RunbookDir + " in the checkout this is run in by default",
			Default: func(x *engine.Ctx) (string, error) {
				top, err := engine.Silently(x, nil, "git", "rev-parse", "--show-toplevel")
				if err != nil || top == "" {
					return "", nil
				}
				return filepath.Join(top, RunbookDir), nil
			}},
	},
	Steps: []engine.Step{{
		Title: "Write the pages",
		Why:   "One page per command and an index, README.md. Pages that would not change are left alone, and pages of commands that no longer exist are removed.",
	}},
}

// Set here rather than above: writeRunbooks lists every flow, Docs among them.
func init() { Docs.Steps[0].Do = engine.Func(writeRunbooks) }

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

func writeRunbooks(x *engine.Ctx) error {
	dir := x.Value("out")
	pages := engine.Runbooks(All())
	changed, gone := staleRunbooks(dir, pages)
	if len(changed) == 0 && len(gone) == 0 {
		x.UI.OK("the runbooks in " + dir + " are current")
		return nil
	}
	for _, name := range changed {
		x.UI.Text("write  " + name)
	}
	for _, name := range gone {
		x.UI.Text("remove " + name)
	}
	if ok, err := x.Approve("Write them to " + dir + "?"); !ok || err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range changed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(pages[name]), 0o644); err != nil {
			return err
		}
	}
	for _, name := range gone {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	x.UI.OK("written; commit them with the change")
	return nil
}
