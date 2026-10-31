// Package book writes the parts of the guide that follow from its order and
// its headings, so that they cannot drift from them: each chapter's number,
// its links to the pages before and after it and to its own sections, the
// contents in the preface, and the index. Everything else on a page is
// written by hand and left as it is.
package book

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The files the book treats as more than chapters.
const (
	Summary  = "SUMMARY.md"       // the order of the book
	Preface  = "README.md"        // the preface, which holds the contents
	Glossary = "glossary.md"      // its terms go in the index
	Index    = "index.md"         // written whole
	Config   = "configuration.md" // its options go in the index
	Accounts = "accounts.md"      // where an account's fields count by name alone
)

// The lines around what is written for the book.
const (
	topStart      = "<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->"
	topEnd        = "<!-- /book:top -->"
	bottomStart   = "<!-- book:bottom -->"
	bottomEnd     = "<!-- /book:bottom -->"
	contentsStart = "<!-- book:contents -->"
	contentsEnd   = "<!-- /book:contents -->"
)

// Chapter is one entry of SUMMARY.md.
type Chapter struct {
	File     string // its file in the guide
	Title    string // the link's text in SUMMARY.md
	Number   string // 1, 2, … for a numbered chapter, A, B, … for an appendix; empty for the preface
	Appendix bool
	Part     string // the part heading it is under, if any
	Line     int    // its line in SUMMARY.md
}

// Name is how the book names a chapter: "1. How it works", "Appendix A.
// Configuration reference", or the preface's title alone.
func (c *Chapter) Name() string {
	switch {
	case c.Appendix:
		return "Appendix " + c.Number + ". " + c.Title
	case c.Number != "":
		return c.Number + ". " + c.Title
	}
	return c.Title
}

// Problem is something wrong at a line of a file.
type Problem struct {
	File string
	Line int
	What string
}

func (p Problem) String() string { return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.What) }

// Command is a fleet command, for the index: its name, such as fleet host
// check, and its runbook's path from the guide.
type Command struct{ Name, Runbook string }

// Book is a guide with its generated parts worked out.
type Book struct {
	Dir      string
	Chapters []*Chapter // in the book's order, the preface first
	// Pages are the pages as they should be, by file name: every chapter
	// that has a file, the preface and the index.
	Pages map[string]string
	// Errors stop the pages from being written: a chapter whose first line
	// is not its heading, a preface without the contents' markers.
	Errors []Problem
	// Warnings are wrong but do not stop it: a page that is not in
	// SUMMARY.md, an entry of SUMMARY.md without its file.
	Warnings []Problem
}

// page is a chapter's file: its heading as it will be, and the text after it
// as written, without the book's blocks and the blank lines around them.
type page struct {
	ch    *Chapter
	path  string
	h1    string // empty for the preface, whose first line is its own
	body  []string
	heads []head // the headings of all
}

// all is the page's lines, its heading among them.
func (p *page) all() []string {
	if p.h1 == "" {
		return p.body
	}
	return append([]string{p.h1}, p.body...)
}

// Make reads the guide in dir and works out its pages. It fails only when
// SUMMARY.md cannot be read or is not in the shape the book needs.
func Make(dir string, cmds []Command) (*Book, error) {
	chapters, err := readSummary(dir)
	if err != nil {
		return nil, err
	}
	b := &Book{Dir: dir, Chapters: chapters, Pages: map[string]string{}}
	pages := map[string]*page{}
	for i, ch := range chapters {
		if ch.File == Index && i > 0 {
			continue // written whole, below
		}
		path := filepath.Join(dir, ch.File)
		text, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			b.Warnings = append(b.Warnings, Problem{filepath.Join(dir, Summary), ch.Line, ch.File + " does not exist"})
			continue
		}
		if err != nil {
			return nil, err
		}
		p, problems := readPage(ch, path, string(text), i == 0)
		b.Errors = append(b.Errors, problems...)
		if p != nil {
			pages[ch.File] = p
		}
	}
	for _, ch := range chapters[1:] {
		if ch.File == Index {
			pages[Index] = newIndexer(chapters, pages, cmds).page(ch)
		}
	}
	for i, ch := range chapters {
		p := pages[ch.File]
		if p == nil {
			continue
		}
		var next *Chapter
		if i+1 < len(chapters) {
			next = chapters[i+1]
		}
		if i == 0 {
			text, problem := preface(p, chapters, pages, next)
			if problem != nil {
				b.Errors = append(b.Errors, *problem)
				continue
			}
			b.Pages[ch.File] = text
			continue
		}
		b.Pages[ch.File] = p.render(chapters[i-1], next)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == Summary || slices.ContainsFunc(chapters, func(c *Chapter) bool { return c.File == name }) {
			continue
		}
		b.Warnings = append(b.Warnings, Problem{filepath.Join(dir, name), 1, "not in " + Summary})
	}
	return b, nil
}

var (
	summaryTitle = regexp.MustCompile(`^# (.+)$`)
	summaryLink  = regexp.MustCompile(`^\[(.+)\]\((.*)\)$`)
	summaryItem  = regexp.MustCompile(`^(\s*)[-*] \[(.+)\]\((.*)\)$`)
)

// readSummary reads the book's order: the preface, numbered chapters under
// part headings, then the appendices.
func readSummary(dir string) ([]*Chapter, error) {
	path := filepath.Join(dir, Summary)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	bad := func(n int, what string) error { return errors.New(Problem{path, n, what}.String()) }
	var chapters []*Chapter
	titled, numbered, appendices := false, 0, 0
	part := ""
	seen := map[string]bool{}
	for i, line := range lines(string(text)) {
		n := i + 1
		line = strings.TrimRight(line, " \t")
		var ch *Chapter
		switch {
		case line == "" || line == "---":
			continue
		case summaryTitle.MatchString(line):
			if !titled && len(chapters) == 0 {
				titled = true // the summary's own title
				continue
			}
			part = summaryTitle.FindStringSubmatch(line)[1]
			continue
		case summaryItem.MatchString(line):
			m := summaryItem.FindStringSubmatch(line)
			if m[1] != "" {
				return nil, bad(n, "a chapter within a chapter, which the book does not number")
			}
			if appendices > 0 {
				return nil, bad(n, "a numbered chapter after the appendices")
			}
			numbered++
			ch = &Chapter{Title: m[2], File: m[3], Number: strconv.Itoa(numbered)}
		case summaryLink.MatchString(line):
			m := summaryLink.FindStringSubmatch(line)
			ch = &Chapter{Title: m[1], File: m[2]}
			if numbered > 0 {
				if appendices == 26 {
					return nil, bad(n, "more than 26 appendices")
				}
				ch.Number, ch.Appendix = string(rune('A'+appendices)), true
				appendices++
			} else if len(chapters) > 0 {
				return nil, bad(n, "a second chapter before the numbered ones; only the preface, "+Preface+", comes before them")
			}
		default:
			return nil, bad(n, "neither a chapter, a part heading nor a blank line")
		}
		if ch.File == "" || strings.Contains(ch.File, "/") || strings.Contains(ch.File, "#") {
			return nil, bad(n, "a chapter is a file in the guide's own directory, not "+strconv.Quote(ch.File))
		}
		if seen[ch.File] {
			return nil, bad(n, ch.File+" is in the book twice")
		}
		seen[ch.File] = true
		ch.Part, ch.Line = part, n
		if len(chapters) == 0 && ch.File != Preface {
			return nil, bad(n, "the book begins with its preface, ["+ch.Title+"]("+Preface+")")
		}
		chapters = append(chapters, ch)
	}
	if len(chapters) < 2 {
		return nil, errors.New(path + ": no chapters")
	}
	chapters[0].Part = ""
	return chapters, nil
}

// readPage takes a page apart: its heading, which a chapter must begin with,
// and the text after it, without the book's blocks.
func readPage(ch *Chapter, path, text string, isPreface bool) (*page, []Problem) {
	ls := lines(text)
	p := &page{ch: ch, path: path}
	if !isPreface {
		if len(ls) == 0 || !strings.HasPrefix(ls[0], "# ") {
			return nil, []Problem{{path, 1, `the first line is not the chapter's heading, "# ` + ch.Title + `"`}}
		}
		p.h1 = "# " + ch.Name()
		ls = ls[1:]
	}
	body, problems := withoutBlocks(path, ls, !isPreface)
	if len(problems) > 0 {
		return nil, problems
	}
	p.body = body
	p.heads = headings(p.all())
	return p, nil
}

// withoutBlocks is a page's lines without its book:top and book:bottom
// blocks, and without the blank lines at either end. The first line of a
// chapter, its heading, is not among them, so off counts it.
func withoutBlocks(path string, ls []string, chapter bool) ([]string, []Problem) {
	off := 1
	if chapter {
		off = 2
	}
	var out []string
	code := codeLines(ls)
	for i := 0; i < len(ls); i++ {
		t := strings.TrimSpace(ls[i])
		end := ""
		switch {
		case code[i]:
		case strings.HasPrefix(t, "<!-- book:top"):
			end = topEnd
		case strings.HasPrefix(t, "<!-- book:bottom"):
			end = bottomEnd
		}
		if end == "" {
			out = append(out, ls[i])
			continue
		}
		j := i + 1
		for j < len(ls) && strings.TrimSpace(ls[j]) != end {
			j++
		}
		if j == len(ls) {
			return nil, []Problem{{path, i + off, "this block has no " + end + " to end it"}}
		}
		i = j
	}
	return trimBlank(out), nil
}

// render writes a chapter: its heading, the links at its top, its text, and
// the links at its bottom.
func (p *page) render(prev, next *Chapter) string {
	links := nav(prev, next, true)
	var b strings.Builder
	b.WriteString(p.h1 + "\n\n")
	b.WriteString(top(links, sections(p.heads)) + "\n\n")
	if len(p.body) > 0 {
		b.WriteString(strings.Join(p.body, "\n") + "\n\n")
	}
	b.WriteString(bottom(links) + "\n")
	return b.String()
}

// nav is the line of links to the chapters before and after, and to the
// contents.
func nav(prev, next *Chapter, contents bool) string {
	var items []string
	if prev != nil {
		items = append(items, "[← "+prev.Name()+"]("+prev.File+")")
	}
	if contents {
		items = append(items, "[Contents]("+Preface+")")
	}
	if next != nil {
		items = append(items, "["+next.Name()+" →]("+next.File+")")
	}
	return strings.Join(items, " · ")
}

// top is the block under a chapter's heading: the links, and its sections.
func top(links string, secs []head) string {
	ls := []string{topStart, links}
	switch {
	case len(secs) == 0:
	case letters(secs):
		var items []string
		for _, s := range secs {
			items = append(items, "["+s.text+"](#"+s.anchor+")")
		}
		ls = append(ls, "", "In this chapter: "+strings.Join(items, " · "))
	default:
		ls = append(ls, "", "In this chapter:", "")
		for _, s := range secs {
			ls = append(ls, "- ["+s.text+"](#"+s.anchor+")")
		}
	}
	return strings.Join(append(ls, topEnd), "\n")
}

// bottom is the block at the end of a page, the links again.
func bottom(links string) string {
	return strings.Join([]string{bottomStart, "---", "", links, bottomEnd}, "\n")
}

// preface writes the contents between its markers, which the hand-written
// preface must have, and the link to the first chapter at its end.
func preface(p *page, chapters []*Chapter, pages map[string]*page, next *Chapter) (string, *Problem) {
	start, end := -1, -1
	code := codeLines(p.body)
	for i, l := range p.body {
		switch {
		case code[i]:
		case strings.TrimSpace(l) == contentsStart && start < 0:
			start = i
		case strings.TrimSpace(l) == contentsEnd && start >= 0 && end < 0:
			end = i
		}
	}
	if end < 0 {
		return "", &Problem{p.path, 1,
			"no " + contentsStart + " and " + contentsEnd + " lines, between which the contents go"}
	}
	ls := slices.Concat(p.body[:start+1], contents(chapters, pages), p.body[end:])
	return strings.Join(ls, "\n") + "\n\n" + bottom(nav(nil, next, false)) + "\n", nil
}

// contents lists the chapters by part, each with its sections; the glossary
// and the index, whose sections are letters, without them.
func contents(chapters []*Chapter, pages map[string]*page) []string {
	var out []string
	part := ""
	for i, ch := range chapters[1:] {
		if i == 0 || ch.Part != part {
			if i > 0 {
				out = append(out, "")
			}
			if ch.Part != "" {
				out = append(out, "### "+ch.Part, "")
			}
			part = ch.Part
		}
		out = append(out, "- ["+ch.Name()+"]("+ch.File+")")
		p := pages[ch.File]
		if p == nil {
			continue
		}
		if secs := sections(p.heads); !letters(secs) {
			for _, s := range secs {
				out = append(out, "  - ["+s.text+"]("+ch.File+"#"+s.anchor+")")
			}
		}
	}
	return out
}

// Change is a page that differs from its file: the first line that does,
// and whether there is no file yet.
type Change struct {
	File string
	Line int
	New  bool
}

// Changed lists the pages that differ from their files, in the book's order.
func (b *Book) Changed() []Change {
	var changes []Change
	for _, ch := range b.Chapters {
		text, ok := b.Pages[ch.File]
		if !ok {
			continue
		}
		path := filepath.Join(b.Dir, ch.File)
		old, err := os.ReadFile(path)
		switch {
		case err != nil:
			changes = append(changes, Change{path, 1, true})
		case string(old) != text:
			changes = append(changes, Change{path, FirstChange(string(old), text), false})
		}
	}
	return changes
}

// FirstChange is the first line, from 1, at which two texts differ.
func FirstChange(old, new string) int {
	a, b := strings.Split(old, "\n"), strings.Split(new, "\n")
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i + 1
		}
	}
	return min(len(a), len(b))
}
