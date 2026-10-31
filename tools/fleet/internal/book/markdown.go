package book

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The little of Markdown the book needs: fenced code, headings and their
// anchors, code spans and links.

// Anchor is the id GitHub gives a heading: its text without backticks, in
// lower case, with only letters, digits, spaces, - and _ kept, and each space
// made a -.
func Anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.ReplaceAll(heading, "`", "")) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fence follows fenced code blocks, a line at a time.
type fence struct{ open string }

// code says whether the line is code: a fence's own line or one between
// fences.
func (f *fence) code(line string) bool {
	t := strings.TrimLeft(line, " \t")
	run := fenceRun(t)
	if f.open == "" {
		if run != "" {
			f.open = run
			return true
		}
		return false
	}
	if run != "" && run[0] == f.open[0] && len(run) >= len(f.open) && strings.TrimSpace(t[len(run):]) == "" {
		f.open = ""
	}
	return true
}

// fenceRun is the ``` or ~~~ (or longer) a line starts with, if any.
func fenceRun(t string) string {
	if !strings.HasPrefix(t, "```") && !strings.HasPrefix(t, "~~~") {
		return ""
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	return t[:n]
}

// codeLines marks the lines that are fenced code.
func codeLines(lines []string) []bool {
	var f fence
	code := make([]bool, len(lines))
	for i, l := range lines {
		code[i] = f.code(l)
	}
	return code
}

var atx = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)

// heading is a line's heading level and text, or 0 when it is not one.
func heading(line string) (int, string) {
	m := atx.FindStringSubmatch(line)
	if m == nil {
		return 0, ""
	}
	return len(m[1]), strings.TrimSpace(m[2])
}

// head is a heading of a page.
type head struct {
	level  int
	text   string
	anchor string
	line   int // its index in the page's lines
}

// headings lists a page's headings outside code, with their anchors; a
// repeated anchor gets -1, -2 and so on, as GitHub numbers them.
func headings(lines []string) []head {
	var hs []head
	seen := map[string]int{}
	code := codeLines(lines)
	for i, l := range lines {
		if code[i] {
			continue
		}
		level, text := heading(l)
		if level == 0 {
			continue
		}
		base := Anchor(text)
		a := base
		for {
			if _, used := seen[a]; !used {
				break
			}
			seen[base]++
			a = base + "-" + strconv.Itoa(seen[base])
		}
		seen[a] = 0
		hs = append(hs, head{level: level, text: text, anchor: a, line: i})
	}
	return hs
}

// sections are the ## headings among hs.
func sections(hs []head) []head {
	var s []head
	for _, h := range hs {
		if h.level == 2 {
			s = append(s, h)
		}
	}
	return s
}

// letters says whether every ## heading is a single letter (or Symbols), as
// in the glossary and the index, whose sections are listed on one line.
func letters(secs []head) bool {
	if len(secs) == 0 {
		return false
	}
	for _, s := range secs {
		if len([]rune(s.text)) != 1 && s.text != symbols {
			return false
		}
		if s.text != symbols && !unicode.IsLetter([]rune(s.text)[0]) {
			return false
		}
	}
	return true
}

// maskCode replaces the code spans of a line with x, so that what is in them
// is not taken for a link; a backtick without its pair stays as it is.
func maskCode(line string) string {
	b := []byte(line)
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
		for j := i; j < end+n; j++ {
			b[j] = 'x'
		}
		i = end + n
	}
	return string(b)
}

// closingRun is where a run of exactly n backticks starts, from i on, or -1.
func closingRun(b []byte, i, n int) int {
	for i < len(b) {
		if b[i] != '`' {
			i++
			continue
		}
		m := 0
		for i+m < len(b) && b[i+m] == '`' {
			m++
		}
		if m == n {
			return i
		}
		i += m
	}
	return -1
}

// link is an inline link or image: [text](target "title").
var link = regexp.MustCompile(`\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(\s*(<[^>]*>|[^()\s]*)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)`)

// linkText is a line with each link replaced by its text.
func linkText(line string) string { return link.ReplaceAllString(line, "$1") }

// lines splits a text into its lines, without the newline after the last.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// trimBlank drops the blank lines at both ends.
func trimBlank(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[0]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}
