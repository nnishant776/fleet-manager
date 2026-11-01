package book

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The index lists every glossary term, conf.* option and fleet command with
// the sections that mention it, the one that defines it first.

const indexIntro = "Every glossary term, `conf.*` option, `fleet` command and host tool, with the sections that mention it. The section that defines it comes first, in bold."

// maxPlaces is the most places an entry lists, its definition among them.
const maxPlaces = 12

// symbols heads the entries that do not start with a letter.
const symbols = "Symbols"

// The kinds of term, which are looked for differently.
const (
	proseTerm   = iota // as a phrase in the text, in any case, outside code
	codeTerm           // as written, in code, once the commands are out of it
	commandTerm        // as written, in code, the longer commands first
)

type term struct {
	show string // as the index lists it
	find string // what is looked for
	kind int
	def  place
	// coded are other forms that count anywhere, such as `fleet.repo.url`;
	// field is an account's field, such as `sshKeys`, which counts in the
	// configuration reference and the chapter on accounts.
	coded []string
	field string
	re    *regexp.Regexp // a prose term's phrase
}

// place is where an entry points: a section of the book, or a page outside
// it, such as a runbook. key is the section's own link, and empty outside.
type place struct{ label, link, key string }

// section is a chapter's opening, before its first ## heading, or a ##
// heading and what follows it.
type section struct {
	ch     *Chapter
	title  string // the heading as written; the chapter's title for the opening
	anchor string // empty for the opening
	lines  []string
	code   []bool
	first  int // the index in the page's lines of its first line
}

func (s *section) link() string {
	if s.anchor == "" {
		return s.ch.File
	}
	return s.ch.File + "#" + s.anchor
}

func (s *section) place() place {
	return place{label: s.ch.Number + " › " + s.title, link: s.link(), key: s.link()}
}

// sections splits a page at its ## headings.
func (p *page) sections() []*section {
	all := p.all()
	code := codeLines(all)
	byLine := map[int]head{}
	for _, h := range p.heads {
		byLine[h.line] = h
	}
	secs := []*section{{ch: p.ch, title: p.ch.Title}}
	for i, l := range all {
		if h, ok := byLine[i]; ok && h.level == 2 {
			secs = append(secs, &section{ch: p.ch, title: h.text, anchor: h.anchor, first: i})
		}
		s := secs[len(secs)-1]
		s.lines = append(s.lines, l)
		s.code = append(s.code, code[i])
	}
	return secs
}

// sectionAt is the section a line of the page is in.
func sectionAt(secs []*section, line int) *section {
	s := secs[0]
	for _, t := range secs {
		if t.first <= line {
			s = t
		}
	}
	return s
}

type indexer struct {
	chapters []*Chapter
	pages    map[string]*page
	terms    []*term
	byKey    map[string]*term
	commands []string // longest first
}

func newIndexer(chapters []*Chapter, pages map[string]*page, cmds []Command) *indexer {
	ix := &indexer{chapters: chapters, pages: pages, byKey: map[string]*term{}}
	ix.options()
	for _, c := range cmds {
		ix.add(&term{show: "`" + c.Name + "`", find: c.Name, kind: commandTerm, def: place{label: "runbook", link: c.Runbook}})
		ix.commands = append(ix.commands, c.Name)
	}
	// Longer commands first, so that fleet host is not found in fleet host
	// enroll.
	slices.SortStableFunc(ix.commands, func(a, b string) int {
		if d := len(strings.Fields(b)) - len(strings.Fields(a)); d != 0 {
			return d
		}
		return len(b) - len(a)
	})
	ix.glossary()
	return ix
}

// add takes a term in, once.
func (ix *indexer) add(t *term) {
	key := strings.ToLower(t.find)
	if _, ok := ix.byKey[key]; ok {
		return
	}
	if t.kind == proseTerm {
		words := strings.Fields(t.find)
		for i, w := range words {
			words[i] = regexp.QuoteMeta(w)
		}
		// Its last word may be plural or possessive: host branches, the
		// recovery identity's.
		t.re = regexp.MustCompile(`(?i)` + strings.Join(words, `\s+`) + `(?:es|s|'s|’s)?`)
	}
	ix.byKey[key] = t
	ix.terms = append(ix.terms, t)
}

var (
	tableRule = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	codeSpan  = regexp.MustCompile("`([^`]+)`")
)

// options are the rows of the configuration reference's tables whose first
// column is Option, and of the one of an account's fields, Field under
// conf.users.
func (ix *indexer) options() {
	p := ix.pages[Config]
	if p == nil {
		return
	}
	all := p.all()
	code := codeLines(all)
	secs := p.sections()
	for i := 0; i+1 < len(all); i++ {
		if code[i] || !isRow(all[i]) || !tableRule.MatchString(all[i+1]) {
			continue
		}
		sec := sectionAt(secs, i)
		first := firstCell(all[i])
		fields := first == "Field" && sec.title == "`conf.users`"
		j := i + 2
		for ; j < len(all) && isRow(all[j]); j++ {
			if first != "Option" && !fields {
				continue
			}
			prev := ""
			for _, m := range codeSpan.FindAllStringSubmatch(firstCell(all[j]), -1) {
				name := m[1]
				if strings.HasPrefix(name, ".") && strings.Contains(prev, ".") {
					name = prev[:strings.LastIndex(prev, ".")] + name
				}
				prev = name
				t := &term{kind: codeTerm, def: sec.place()}
				if fields {
					full := "users.accounts.<name>." + name
					t.show, t.find, t.coded, t.field = "`conf."+full+"`", "conf."+full, []string{"`" + full + "`"}, "`"+name+"`"
				} else {
					t.show, t.find, t.coded = "`conf."+name+"`", "conf."+name, []string{"`" + name + "`"}
				}
				ix.add(t)
			}
		}
		i = j - 1
	}
}

func isRow(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "|") }

// firstCell is a table row's first cell.
func firstCell(row string) string {
	row = strings.TrimPrefix(strings.TrimSpace(row), "|")
	for i := 0; i < len(row); i++ {
		switch row[i] {
		case '\\':
			i++
		case '|':
			return strings.TrimSpace(row[:i])
		}
	}
	return strings.TrimSpace(row)
}

var glossaryLine = regexp.MustCompile(`^- \*\*(.+?)\*\*: `)

// glossary takes the terms of the glossary, each defined where the first
// link of its entry points, or in its letter's section.
func (ix *indexer) glossary() {
	p := ix.pages[Glossary]
	if p == nil {
		return
	}
	all := p.all()
	code := codeLines(all)
	secs := p.sections()
	for i, l := range all {
		m := glossaryLine.FindStringSubmatch(l)
		if code[i] || m == nil {
			continue
		}
		// The entry goes on until a blank line, the next entry or a heading.
		entry := l
		for j := i + 1; j < len(all) && !code[j] && strings.TrimSpace(all[j]) != "" &&
			!strings.HasPrefix(all[j], "- ") && !strings.HasPrefix(all[j], "#"); j++ {
			entry += "\n" + all[j]
		}
		t := &term{show: m[1], kind: proseTerm, find: strings.ReplaceAll(m[1], "`", "")}
		if strings.Count(m[1], "`") == 2 && strings.HasPrefix(m[1], "`") && strings.HasSuffix(m[1], "`") {
			t.kind = codeTerm
		}
		loc := link.FindStringSubmatchIndex(maskCode(entry))
		if loc != nil {
			t.def = ix.placeOf(entry[loc[4]:loc[5]], entry[loc[2]:loc[3]])
		} else {
			t.def = sectionAt(secs, i).place()
		}
		// An option or a command the glossary has as well is listed once, as
		// what it is, defined where the glossary links to when it does.
		if old := ix.byKey[strings.ToLower(t.find)]; old != nil {
			if loc != nil {
				old.def = t.def
			}
			continue
		}
		ix.add(t)
	}
}

// placeOf is where a link of the glossary's points: a section of the book,
// named as the index names them, or a page outside it, named as the link is.
func (ix *indexer) placeOf(target, text string) place {
	target = strings.Trim(target, "<>")
	file, anchor, _ := strings.Cut(target, "#")
	if file == "" {
		file = Glossary
	} else {
		file = path.Clean(file)
	}
	link := file
	if anchor != "" {
		link += "#" + anchor
	}
	p := ix.pages[file]
	if p == nil || p.ch.Number == "" {
		return place{label: text, link: link}
	}
	secs := p.sections()
	if anchor == "" {
		return place{label: secs[0].place().label, link: link, key: secs[0].link()}
	}
	for _, h := range p.heads {
		if h.anchor == anchor {
			s := sectionAt(secs, h.line).place()
			s.link = link
			return s
		}
	}
	return place{label: text, link: link}
}

// text is a section as the terms are looked for in it.
type text struct {
	raw      string          // every line, links as their text
	prose    string          // outside code, without backticks or commands
	code     string          // fenced code and what is in code spans, without commands
	commands map[string]bool // the commands its code names
}

func (ix *indexer) textOf(s *section) text {
	var raw, prose, code, para []string
	// Code spans are found a paragraph at a time, as one may go on to the
	// next line.
	spans := func() {
		code = append(code, codeSpans(strings.Join(para, "\n"))...)
		para = nil
	}
	for i, l := range s.lines {
		if s.code[i] {
			spans()
			raw = append(raw, l)
			code = append(code, l)
			continue
		}
		if strings.TrimSpace(l) == "" {
			spans()
		} else {
			para = append(para, l)
		}
		l = linkText(l)
		raw = append(raw, l)
		prose = append(prose, strings.ReplaceAll(l, "`", ""))
	}
	spans()
	t := text{raw: strings.Join(raw, "\n")}
	t.code, t.commands = ix.blankCommands(strings.Join(code, "\n"))
	t.prose, _ = ix.blankCommands(strings.Join(prose, "\n"))
	return t
}

// codeSpans is what the code spans of a paragraph hold, each on a line of
// its own.
func codeSpans(s string) []string {
	var spans []string
	b := []byte(s)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(b) && b[i+n] == '`' {
			n++
		}
		end := closingRun(b, i+n, n)
		if end < 0 {
			i += n
			continue
		}
		spans = append(spans, strings.ReplaceAll(s[i+n:end], "\n", " "))
		i = end + n
	}
	return spans
}

// blankCommands finds the commands in a text, the longer first, and blanks
// each out once found.
func (ix *indexer) blankCommands(s string) (string, map[string]bool) {
	found := map[string]bool{}
	for _, c := range ix.commands {
		b := []byte(s)
		for i := codeAt(s, c, 0); i >= 0; i = codeAt(s, c, i+len(c)) {
			found[c] = true
			for j := i; j < i+len(c); j++ {
				b[j] = ' '
			}
		}
		s = string(b)
	}
	return s, found
}

// codeAt is where code is written in s from i on, not within a longer word,
// path or name; -1 when it is not.
func codeAt(s, code string, i int) int {
	for i <= len(s) {
		j := strings.Index(s[i:], code)
		if j < 0 {
			return -1
		}
		j += i
		before, _ := utf8.DecodeLastRuneInString(s[:j])
		after, _ := utf8.DecodeRuneInString(s[j+len(code):])
		if (j == 0 || !nameRune(before) || before == '.') && (j+len(code) == len(s) || !nameRune(after)) {
			return j
		}
		i = j + 1
	}
	return -1
}

// nameRune is what continues a name: a letter, a digit, ., - or _.
func nameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_'
}

// wordRune is what continues a word of prose: a letter, a digit, - or _.
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'
}

// hasPhrase says whether the phrase is in s as words of their own.
func hasPhrase(s string, re *regexp.Regexp) bool {
	for i := 0; i <= len(s); {
		loc := re.FindStringIndex(s[i:])
		if loc == nil {
			return false
		}
		start, end := i+loc[0], i+loc[1]
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if (start == 0 || !wordRune(before)) && (end == len(s) || !wordRune(after)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[start:])
		i = start + max(size, 1)
	}
	return false
}

// mentions says whether a section mentions a term.
func (t *term) mentions(s *section, tx text) bool {
	switch t.kind {
	case proseTerm:
		return hasPhrase(tx.prose, t.re)
	case commandTerm:
		return tx.commands[t.find]
	}
	if codeAt(tx.code, t.find, 0) >= 0 {
		return true
	}
	for _, c := range t.coded {
		if strings.Contains(tx.raw, c) {
			return true
		}
	}
	return t.field != "" && (s.ch.File == Config || s.ch.File == Accounts) && strings.Contains(tx.raw, t.field)
}

// page writes the index, a chapter of its own.
func (ix *indexer) page(ch *Chapter) *page {
	var secs []*section
	for _, c := range ix.chapters[1:] {
		if p := ix.pages[c.File]; p != nil && c.File != Glossary && c.File != Index {
			secs = append(secs, p.sections()...)
		}
	}
	texts := make([]text, len(secs))
	for i, s := range secs {
		texts[i] = ix.textOf(s)
	}
	terms := slices.Clone(ix.terms)
	slices.SortStableFunc(terms, func(a, b *term) int {
		ga, gb := group(a), group(b)
		if (ga == symbols) != (gb == symbols) {
			if ga == symbols {
				return -1
			}
			return 1
		}
		return strings.Compare(sortKey(a), sortKey(b))
	})
	body := []string{indexIntro}
	g := ""
	for _, t := range terms {
		if group(t) != g {
			g = group(t)
			body = append(body, "", "## "+g, "")
		}
		places := []string{"**[" + t.def.label + "](" + t.def.link + ")**"}
		for i, s := range secs {
			if len(places) == maxPlaces {
				break
			}
			if s.link() != t.def.key && t.mentions(s, texts[i]) {
				p := s.place()
				places = append(places, "["+p.label+"]("+p.link+")")
			}
		}
		body = append(body, "- "+t.show+": "+strings.Join(places, ", "))
	}
	p := &page{ch: ch, h1: "# " + ch.Name(), body: body}
	p.heads = headings(p.all())
	return p
}

// sortKey orders the entries: in any case, without backticks or a leading /.
func sortKey(t *term) string {
	return strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(t.show, "`", ""), "/"))
}

// group is the letter an entry is listed under, or Symbols.
func group(t *term) string {
	r, _ := utf8.DecodeRuneInString(sortKey(t))
	if !unicode.IsLetter(r) {
		return symbols
	}
	return string(unicode.ToUpper(r))
}
