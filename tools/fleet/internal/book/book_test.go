package book

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAnchorIsGitHubs(t *testing.T) {
	for heading, want := range map[string]string{
		"The tools' own clones":                            "the-tools-own-clones",
		"`conf.role`, `conf.machineType`, `conf.platform`": "confrole-confmachinetype-confplatform",
		"From `conf.*` to a system":                        "from-conf-to-a-system",
		"Maintainer: one-time setup":                       "maintainer-one-time-setup",
		"Day-2 runbooks":                                   "day-2-runbooks",
		"Über_alles (ja)":                                  "über_alles-ja",
	} {
		if got := Anchor(heading); got != want {
			t.Errorf("Anchor(%q) = %q, want %q", heading, got, want)
		}
	}
}

func TestHeadingsNumberRepeatsAndSkipCode(t *testing.T) {
	hs := headings(lines("# Top\n## A\n```\n## not one\n```\n### A\n## A ##\n~~~~\n```\n# nor this\n~~~~\n## B"))
	var got []string
	for _, h := range hs {
		got = append(got, h.anchor)
	}
	if want := []string{"top", "a", "a-1", "a-2", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("anchors %v, want %v", got, want)
	}
}

// writeGuide writes a book into a guide directory of a new checkout; a name
// may reach out of it, as ../README.md does.
func writeGuide(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "guide")
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const smallSummary = `# Summary

[Preface](README.md)

# Part I. Basics

- [First](first.md)
- [Second](second.md)

# Part II. More

- [Third](third.md)

# Appendices

[Reference](reference.md)
[Words](words.md)
[Index](index.md)
`

func smallBook() map[string]string {
	return map[string]string{
		"SUMMARY.md": smallSummary,
		"README.md":  "# The book\n\nWritten by hand.\n\n## Contents\n\n<!-- book:contents -->\nwhatever was here\n<!-- /book:contents -->\n",
		"first.md": "# An old title\n\nThe opening.  \n\n## The pieces\n\nText.\n\n```\n## not a section\n<!-- book:top -->\n```\n\n" +
			"### Deeper\n\n## The pieces\n\nMore.\n",
		"second.md":    "# Second\nNo sections, and no blank line under the heading.",
		"third.md":     "# Third\n\n## `conf.*`, and more\n\nText.\n",
		"reference.md": "# Reference\n\n## One\n",
		"words.md":     "# Words\n\n## A\n\n- **apple**: A fruit.\n\n## B\n\n- **berry**: Another.\n",
	}
}

func makeBook(t *testing.T, dir string) *Book {
	t.Helper()
	b, err := Make(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Errors) > 0 {
		t.Fatalf("errors: %v", b.Errors)
	}
	return b
}

func TestChaptersAreNumberedAcrossPartsAndAppendicesLettered(t *testing.T) {
	b := makeBook(t, writeGuide(t, smallBook()))
	var got []string
	for _, c := range b.Chapters {
		got = append(got, c.Name()+" | "+c.Part)
	}
	want := []string{
		"Preface | ",
		"1. First | Part I. Basics",
		"2. Second | Part I. Basics",
		"3. Third | Part II. More",
		"Appendix A. Reference | Appendices",
		"Appendix B. Words | Appendices",
		"Appendix C. Index | Appendices",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chapters\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEachChapterGetsItsNumberLinksAndSections(t *testing.T) {
	b := makeBook(t, writeGuide(t, smallBook()))
	nav := "[← Preface](README.md) · [Contents](README.md) · [2. Second →](second.md)"
	want := "# 1. First\n\n" + topStart + "\n" + nav + "\n\nIn this chapter:\n\n" +
		"- [The pieces](#the-pieces)\n- [The pieces](#the-pieces-1)\n" + topEnd + "\n\n" +
		"The opening.  \n\n## The pieces\n\nText.\n\n```\n## not a section\n<!-- book:top -->\n```\n\n" +
		"### Deeper\n\n## The pieces\n\nMore.\n\n" +
		bottomStart + "\n---\n\n" + nav + "\n" + bottomEnd + "\n"
	if got := b.Pages["first.md"]; got != want {
		t.Errorf("first.md:\n%s\nwant:\n%s", got, want)
	}

	// The middle, across a part; a page without sections lists none.
	nav = "[← 1. First](first.md) · [Contents](README.md) · [3. Third →](third.md)"
	want = "# 2. Second\n\n" + topStart + "\n" + nav + "\n" + topEnd + "\n\n" +
		"No sections, and no blank line under the heading.\n\n" +
		bottomStart + "\n---\n\n" + nav + "\n" + bottomEnd + "\n"
	if got := b.Pages["second.md"]; got != want {
		t.Errorf("second.md:\n%s\nwant:\n%s", got, want)
	}

	// An appendix, after the last numbered chapter; a section in code.
	if got := b.Pages["reference.md"]; !strings.HasPrefix(got, "# Appendix A. Reference\n\n"+topStart+"\n"+
		"[← 3. Third](third.md) · [Contents](README.md) · [Appendix B. Words →](words.md)\n") {
		t.Errorf("reference.md:\n%s", got)
	}
	if got := b.Pages["third.md"]; !strings.Contains(got, "- [`conf.*`, and more](#conf-and-more)\n") {
		t.Errorf("third.md:\n%s", got)
	}

	// Sections of single letters go on one line; the last page has no next.
	if got := b.Pages["words.md"]; !strings.Contains(got, "\n\nIn this chapter: [A](#a) · [B](#b)\n"+topEnd+"\n") {
		t.Errorf("words.md:\n%s", got)
	}
	if got := b.Pages["index.md"]; !strings.Contains(got, "\n---\n\n[← Appendix B. Words](words.md) · [Contents](README.md)\n"+bottomEnd+"\n") {
		t.Errorf("index.md:\n%s", got)
	}
}

func TestPrefaceGetsTheContentsAndALinkToTheFirstChapter(t *testing.T) {
	b := makeBook(t, writeGuide(t, smallBook()))
	want := "# The book\n\nWritten by hand.\n\n## Contents\n\n" + contentsStart + "\n" +
		"### Part I. Basics\n\n" +
		"- [1. First](first.md)\n  - [The pieces](first.md#the-pieces)\n  - [The pieces](first.md#the-pieces-1)\n" +
		"- [2. Second](second.md)\n\n" +
		"### Part II. More\n\n" +
		"- [3. Third](third.md)\n  - [`conf.*`, and more](third.md#conf-and-more)\n\n" +
		"### Appendices\n\n" +
		"- [Appendix A. Reference](reference.md)\n  - [One](reference.md#one)\n" +
		"- [Appendix B. Words](words.md)\n" +
		"- [Appendix C. Index](index.md)\n" +
		contentsEnd + "\n\n" +
		bottomStart + "\n---\n\n[1. First →](first.md)\n" + bottomEnd + "\n"
	if got := b.Pages["README.md"]; got != want {
		t.Errorf("README.md:\n%s\nwant:\n%s", got, want)
	}
}

// Written once, the pages are what a second run would write; written over
// pages with old blocks, nothing of those is left.
func TestWritingTwiceChangesNothing(t *testing.T) {
	dir := writeGuide(t, smallBook())
	first := makeBook(t, dir)
	if len(first.Changed()) != len(first.Pages) {
		t.Fatalf("changed %v of %d pages", first.Changed(), len(first.Pages))
	}
	for name, text := range first.Pages {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second := makeBook(t, dir)
	if c := second.Changed(); len(c) > 0 {
		t.Errorf("changed again: %v", c)
	}
	if !reflect.DeepEqual(first.Pages, second.Pages) {
		t.Error("the second run's pages differ")
	}

	// A page whose blocks are from another order of the book.
	stale := strings.ReplaceAll(first.Pages["second.md"], "3. Third", "9. Gone")
	stale = strings.Replace(stale, topEnd+"\n\n", topEnd+"\n\n\n\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "second.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	third := makeBook(t, dir)
	if got := third.Pages["second.md"]; got != first.Pages["second.md"] {
		t.Errorf("rewritten:\n%s\nwant:\n%s", got, first.Pages["second.md"])
	}
}

func TestProblemsThatStopTheWriting(t *testing.T) {
	files := smallBook()
	files["second.md"] = "No heading here.\n"
	files["README.md"] = "# The book\n\nNo markers.\n"
	files["third.md"] = "# Third\n\nText.\n\n<!-- book:bottom -->\n---\n"
	dir := writeGuide(t, files)
	b, err := Make(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range b.Errors {
		got = append(got, strings.TrimPrefix(p.String(), dir+"/"))
	}
	want := []string{
		`README.md:1: no ` + contentsStart + ` and ` + contentsEnd + ` lines, between which the contents go`,
		`second.md:1: the first line is not the chapter's heading, "# Second"`,
		`third.md:5: this block has no ` + bottomEnd + ` to end it`,
	}
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, name := range []string{"README.md", "second.md", "third.md"} {
		if _, ok := b.Pages[name]; ok {
			t.Errorf("%s would be written", name)
		}
	}
}

func TestPagesAndSummaryMustAgree(t *testing.T) {
	files := smallBook()
	delete(files, "words.md")
	files["stray.md"] = "# Stray\n"
	files["notes.txt"] = "not Markdown"
	dir := writeGuide(t, files)
	b := makeBook(t, dir)
	var got []string
	for _, p := range b.Warnings {
		got = append(got, strings.TrimPrefix(p.String(), dir+"/"))
	}
	// The index has no file yet either, but fleet docs writes it.
	want := []string{"SUMMARY.md:17: words.md does not exist", "stray.md:1: not in SUMMARY.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings %v, want %v", got, want)
	}
	if c := b.Changed(); c[len(c)-1].File != filepath.Join(dir, "index.md") || !c[len(c)-1].New {
		t.Errorf("changed %v", c)
	}
}

func TestSummaryInTheWrongShape(t *testing.T) {
	for summary, want := range map[string]string{
		"[Preface](README.md)\n- [One](one.md)\n  - [Inner](inner.md)\n":      "SUMMARY.md:3: a chapter within a chapter",
		"[Preface](README.md)\n- [One](one.md)\n[A](a.md)\n- [Two](two.md)\n": "SUMMARY.md:4: a numbered chapter after the appendices",
		"[Preface](README.md)\nSome words\n":                                  "SUMMARY.md:2: neither a chapter",
		"- [One](one.md)\n":                                                   "SUMMARY.md:1: the book begins with its preface",
		"[Preface](README.md)\n[Foreword](foreword.md)\n- [One](one.md)\n":    "SUMMARY.md:2: a second chapter before the numbered ones",
		"[Preface](README.md)\n- [One](one.md)\n- [Again](one.md)\n":          "SUMMARY.md:3: one.md is in the book twice",
		"[Preface](README.md)\n- [Draft]()\n":                                 "SUMMARY.md:2: a chapter is a file in the guide's own directory",
	} {
		dir := writeGuide(t, map[string]string{"SUMMARY.md": summary})
		_, err := Make(dir, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", summary, err, want)
		}
	}
}
