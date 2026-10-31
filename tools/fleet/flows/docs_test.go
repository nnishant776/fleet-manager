package flows

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// The runbooks in the repository are the ones the flows write.
func TestRunbooksAreCurrent(t *testing.T) {
	changed, gone := staleRunbooks("../runbooks", engine.Runbooks(All()))
	if len(changed) > 0 || len(gone) > 0 {
		t.Errorf("the runbooks are out of date (to write: %v, to remove: %v): run `go run . docs --yes` in tools/fleet and commit them",
			changed, gone)
	}
}

func TestRunbookShowsFlagsStepsAndCommands(t *testing.T) {
	page := engine.Runbook(SupportKeyRotate)
	for _, want := range []string{
		"# fleet support-key rotate\n",
		"fleet support-key rotate [HOST] [flags]",
		"| `--old-key` | Its private key file |",
		"5. **Sync HOST with the old key.**",
		"   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i OLD-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'",
		"whoever holds it is still root on HOST.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if install := engine.Runbook(Install); !strings.Contains(install, "| `-d`, `--disk` |") ||
		!strings.Contains(install, "may be given more than once") {
		t.Errorf("install's flags:\n%s", install)
	}
}

// Every flow is on the menu once, but docs, which is for changing fleet.
func TestMenuListsEveryFlowOnce(t *testing.T) {
	seen := map[*engine.Flow]int{}
	for _, s := range Menu {
		for _, f := range s.Flows {
			seen[f]++
		}
	}
	for _, f := range All() {
		want := 1
		if f == Docs {
			want = 0
		}
		if seen[f] != want {
			t.Errorf("fleet %s is on the menu %d times, want %d", strings.Join(f.Path, " "), seen[f], want)
		}
	}
}

// docsCheckout is a checkout with a small book in docs/ and no runbooks yet.
func docsCheckout(t *testing.T) (top string, flags map[string]string) {
	top = t.TempDir()
	for name, text := range map[string]string{
		"README.md":          "[The guide](docs/README.md)\n",
		"docs/SUMMARY.md":    "[Preface](README.md)\n- [One](one.md)\n[Index](index.md)\n",
		"docs/README.md":     "# Guide\n\n<!-- book:contents -->\n<!-- /book:contents -->\n",
		"docs/one.md":        "# One\n\nRun `fleet docs` after a change.\n",
		RunbookDir + "/x.md": "a page of a command that is gone\n",
	} {
		path := filepath.Join(top, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return top, map[string]string{"out": filepath.Join(top, RunbookDir), "guide": filepath.Join(top, GuideDir)}
}

func runDocs(t *testing.T, flags map[string]string, check bool) (string, error) {
	t.Helper()
	x, _, out := newFakeCtx(nil)
	given := maps.Clone(flags)
	if check {
		given["check"] = "true"
	}
	err := engine.RunWith(x, Docs, given)
	return out.String(), err
}

func TestDocsCheckFailsUntilThePagesAreWritten(t *testing.T) {
	top, flags := docsCheckout(t)
	out, err := runDocs(t, flags, true)
	if err == nil {
		t.Fatalf("the check passed:\n%s", out)
	}
	for _, want := range []string{
		"✗ tools/fleet/runbooks/docs.md:1: missing; fleet docs writes it",
		"✗ tools/fleet/runbooks/x.md:1: no command writes it; fleet docs removes it",
		"✗ docs/README.md:4: out of date; fleet docs rewrites it",
		"✗ docs/one.md:1: out of date; fleet docs rewrites it",
		"✗ docs/index.md:1: missing; fleet docs writes it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(top, GuideDir, "index.md")); err == nil {
		t.Error("the check wrote the index")
	}

	if out, err := runDocs(t, flags, false); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(top, RunbookDir, "x.md")); err == nil {
		t.Error("the page of a command that is gone is still there")
	}
	if out, err := runDocs(t, flags, true); err != nil || !strings.Contains(out, "every link leads somewhere") {
		t.Fatalf("after writing, the check: %v\n%s", err, out)
	}

	// The maintainer's local plans, in a folder inside the book's, are not pages
	// of the book, and their links are not checked.
	if err := os.MkdirAll(filepath.Join(top, GuideDir, "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, GuideDir, "plans", "a-plan.md"), []byte("# A plan\n\nSee [gone](gone.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runDocs(t, flags, true); err != nil {
		t.Fatalf("with plans in the book's folder, the check: %v\n%s", err, out)
	}
	index, _ := os.ReadFile(filepath.Join(top, GuideDir, "index.md"))
	if !strings.Contains(string(index), "- `fleet docs`: **[runbook](../tools/fleet/runbooks/docs.md)**, [1 › One](one.md)\n") {
		t.Errorf("index:\n%s", index)
	}

	// A link that leads nowhere and a page the book does not list fail the
	// check, and are only shown when the pages are written.
	one := filepath.Join(top, GuideDir, "one.md")
	text, _ := os.ReadFile(one)
	broken := strings.Replace(string(text), "after a change.", "after a change; see [gone](gone.md).", 1)
	if err := os.WriteFile(one, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, GuideDir, "stray.md"), []byte("# Stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runDocs(t, flags, true)
	if err == nil || !strings.Contains(out, "✗ docs/one.md:7: links to gone.md, which does not exist") ||
		!strings.Contains(out, "✗ docs/stray.md:1: not in SUMMARY.md") || strings.Contains(out, "out of date") {
		t.Errorf("the check: %v\n%s", err, out)
	}
	if out, err := runDocs(t, flags, false); err != nil || !strings.Contains(out, "docs/one.md:7: links to gone.md") {
		t.Errorf("writing: %v\n%s", err, out)
	}
}

// A preface without the contents' markers stops it before anything is
// written, runbooks or pages.
func TestDocsWritesNothingWhileAPageCannotBeWritten(t *testing.T) {
	top, flags := docsCheckout(t)
	if err := os.WriteFile(filepath.Join(top, GuideDir, "README.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runDocs(t, flags, false)
	if err == nil || !strings.Contains(out, "✗ docs/README.md:1: no <!-- book:contents --> and <!-- /book:contents --> lines") {
		t.Errorf("%v:\n%s", err, out)
	}
	for _, name := range []string{RunbookDir + "/docs.md", "docs/index.md"} {
		if _, err := os.Stat(filepath.Join(top, name)); err == nil {
			t.Errorf("%s was written", name)
		}
	}
}
