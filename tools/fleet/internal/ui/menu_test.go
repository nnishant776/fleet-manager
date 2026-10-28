package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func testRows() []MenuRow {
	long := Guide{Usage: "fleet host check [HOST]", About: []string{"Logs in to the host."}}
	for i := 1; i <= 12; i++ {
		long.Steps = append(long.Steps, GuideItem{
			Name: fmt.Sprintf("Step number %d", i),
			Text: "Because this is the reason for the step and it is long enough to wrap around the panel.",
		})
	}
	return []MenuRow{
		{Section: "Hosts", Label: "Check a host", Path: "host check", Guide: long},
		{Label: "Add or change a host secret that has a long title", Path: "host secret set",
			Guide: Guide{Usage: "fleet host secret set", Flags: []GuideItem{{Name: "--secret-stdin", Text: "read it"}}}},
		{Section: "This machine", Label: "Show the status", Path: "status", Guide: Guide{Usage: "fleet status"}},
		{Label: "Rotate a secret", Path: "secret rotate", Guide: Guide{Usage: "fleet secret rotate"}},
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func newTest(w, h int, look Look) *menuModel {
	m := newMenu("What do you want to do?", testRows(), 0, look)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func send(m *menuModel, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

func typeText(m *menuModel, s string) {
	for _, r := range s {
		send(m, string(r))
	}
}

func screen(m *menuModel) []string {
	return strings.Split(ansi.Strip(m.View().Content), "\n")
}

func has(t *testing.T, lines []string, want string) {
	t.Helper()
	if !strings.Contains(strings.Join(lines, "\n"), want) {
		t.Errorf("missing %q in:\n%s", want, strings.Join(lines, "\n"))
	}
}

func lacks(t *testing.T, lines []string, want string) {
	t.Helper()
	if strings.Contains(strings.Join(lines, "\n"), want) {
		t.Errorf("unexpected %q in:\n%s", want, strings.Join(lines, "\n"))
	}
}

func TestMenuTwoPanels(t *testing.T) {
	for _, size := range [][2]int{{80, 25}, {120, 40}} {
		m := newTest(size[0], size[1], Look{})
		lines := screen(m)
		if len(lines) != size[1] {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%v: line of width %d: %q", size, w, l)
			}
		}
		has(t, lines, " fleet · What do you want to do?")
		has(t, lines, "╭ Commands")
		has(t, lines, "╭ Check a host")
		has(t, lines, "› Check a host")
		has(t, lines, "fleet host check [HOST]")
		has(t, lines, "Steps")
		want := "↑↓ move · enter run · / filter · d hide docs · < > width · ? keys · q quit"
		if size[0] >= 100 {
			want = "↑↓ move · enter run · / filter · d hide docs · tab docs · pgup/pgdn scroll · < > width · ? keys · q quit"
		}
		if !strings.Contains(lines[len(lines)-1], want) {
			t.Errorf("key line: %q, want %q", lines[len(lines)-1], want)
		}
		if !m.View().AltScreen {
			t.Error("not on the alternate screen")
		}
	}
}

func TestMenuNarrowShowsOnePanel(t *testing.T) {
	m := newTest(60, 20, Look{})
	lines := screen(m)
	if len(lines) != 20 {
		t.Errorf("%d lines", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("too wide: %q", l)
		}
	}
	has(t, lines, "╭ Commands")
	lacks(t, lines, "fleet host check [HOST]")
	send(m, "tab")
	lines = screen(m)
	has(t, lines, "fleet host check [HOST]")
	lacks(t, lines, "╭ Commands")
	send(m, "tab")
	has(t, screen(m), "╭ Commands")
}

func TestMenuMovesPastSections(t *testing.T) {
	m := newTest(80, 25, Look{})
	send(m, "down")
	if m.cursor != 1 {
		t.Fatalf("cursor %d", m.cursor)
	}
	send(m, "down") // skips the "This machine" row
	if m.cursor != 2 {
		t.Fatalf("cursor %d", m.cursor)
	}
	send(m, "up", "up", "up")
	if m.cursor != 0 {
		t.Fatalf("no wrap at the top: cursor %d", m.cursor)
	}
	send(m, "end")
	if m.cursor != 3 {
		t.Fatalf("end: cursor %d", m.cursor)
	}
	send(m, "down")
	if m.cursor != 3 {
		t.Fatalf("no wrap at the bottom: cursor %d", m.cursor)
	}
	send(m, "home")
	if m.cursor != 0 {
		t.Fatalf("home: cursor %d", m.cursor)
	}
	send(m, "j", "j", "k")
	if m.cursor != 1 {
		t.Fatalf("j/k: cursor %d", m.cursor)
	}
}

func TestMenuEnterAndQuit(t *testing.T) {
	m := newTest(80, 25, Look{})
	send(m, "end", "enter")
	if i, err := m.result(); err != nil || i != 3 {
		t.Errorf("chose %d, %v", i, err)
	}
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := newTest(80, 25, Look{})
		_, cmd := m.Update(key(k))
		if _, err := m.result(); err != ErrCancelled || cmd == nil {
			t.Errorf("%s: err %v, cmd %v", k, err, cmd)
		}
	}
}

func TestMenuFilter(t *testing.T) {
	m := newTest(80, 25, Look{})
	send(m, "/")
	typeText(m, "secret")
	lines := screen(m)
	has(t, lines, "filter: secret▏ · esc clear")
	has(t, lines, "Add or change a host secret")
	has(t, lines, "Rotate a secret")
	lacks(t, lines, "Check a host")
	lacks(t, lines, "Show the status")
	if m.cursor != 1 {
		t.Errorf("the cursor did not move to the first match: %d", m.cursor)
	}
	// The path matches too, and case does not matter.
	send(m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	typeText(m, "HOST CH")
	has(t, screen(m), "Check a host")
	lacks(t, screen(m), "Rotate a secret")
	// q is text while filtering.
	send(m, "esc")
	if m.done || m.filter != "" {
		t.Fatalf("esc did not clear the filter: %+v", m)
	}
	has(t, screen(m), "This machine")
	send(m, "/")
	typeText(m, "rotate")
	send(m, "enter")
	if i, err := m.result(); err != nil || i != 3 {
		t.Errorf("chose %d, %v", i, err)
	}
	m = newTest(80, 25, Look{})
	send(m, "/")
	typeText(m, "q")
	if m.done {
		t.Error("q quit while filtering")
	}
	send(m, "esc", "esc")
	if _, err := m.result(); err != ErrCancelled {
		t.Errorf("second esc: %v", err)
	}
	m = newTest(80, 25, Look{})
	send(m, "/")
	typeText(m, "zzz")
	has(t, screen(m), "no match")
}

func TestMenuGuideScrolls(t *testing.T) {
	m := newTest(80, 25, Look{})
	lines := screen(m)
	has(t, lines, "Step number 1")
	has(t, lines, "↓ more (pgdn)")
	lacks(t, lines, "↑ (pgup)")
	send(m, "pgdown")
	lines = screen(m)
	lacks(t, lines, "Step number 1\n")
	has(t, lines, "↑ (pgup)")
	if m.scroll == 0 {
		t.Fatal("did not scroll")
	}
	send(m, "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
	lines = screen(m)
	has(t, lines, "Step number 12")
	lacks(t, lines, "↓ more")
	// Another command starts at the top.
	send(m, "down")
	if m.scroll != 0 {
		t.Errorf("scroll %d after changing command", m.scroll)
	}
	// In the guide, the arrows scroll it.
	send(m, "up", "tab", "down")
	if m.scroll != 1 || m.cursor != 0 {
		t.Errorf("scroll %d, cursor %d", m.scroll, m.cursor)
	}
}

func TestMenuLongTitles(t *testing.T) {
	m := newTest(80, 25, Look{})
	lines := screen(m)
	has(t, lines, "Add or change a host secret…")
	lacks(t, lines, "Add or change a host secret that")
	send(m, "down")
	has(t, screen(m), "╭ Add or change a host secret that has a long… ")
	wide := newTest(120, 30, Look{})
	send(wide, "down")
	has(t, screen(wide), "╭ Add or change a host secret that has a long title ─")
}

func TestMenuLooks(t *testing.T) {
	console := newTest(80, 25, Look{Console: true})
	lines := screen(console)
	has(t, lines, "┌ Commands")
	lacks(t, lines, "╭")
	has(t, lines, "> Check a host")
	has(t, lines, "up/down move | enter run | / filter | d hide docs | < > width | ? keys | q quit")
	has(t, lines, "v more (pgdn)")
	has(t, lines, "Add or change a host secr...")
	for _, l := range lines {
		for _, r := range l {
			if r > 0x7f && !strings.ContainsRune("┌┐└┘─│", r) {
				t.Errorf("non-ASCII %q on the console: %q", r, l)
			}
		}
	}
	rich := screen(newTest(80, 25, Look{}))
	has(t, rich, "╭")
}

func TestMenuHighlightBar(t *testing.T) {
	bar := func(m *menuModel) string {
		for _, l := range strings.Split(m.View().Content, "\n") {
			if strings.Contains(ansi.Strip(l), "› Check a host") {
				return l
			}
		}
		t.Fatal("no highlighted row")
		return ""
	}
	m := newTest(80, 25, Look{})
	if l := bar(m); !strings.Contains(l, "48;5;238") {
		t.Errorf("dark bar missing: %q", l)
	}
	m.Update(tea.BackgroundColorMsg{Color: lightColour{}})
	if l := bar(m); !strings.Contains(l, "48;5;252") {
		t.Errorf("light bar missing: %q", l)
	}
	// FLEET_THEME wins over the answer.
	m = newTest(80, 25, Look{Theme: "dark"})
	m.Update(tea.BackgroundColorMsg{Color: lightColour{}})
	if l := bar(m); !strings.Contains(l, "48;5;238") {
		t.Errorf("FLEET_THEME ignored: %q", l)
	}
}

type lightColour struct{}

func (lightColour) RGBA() (r, g, b, a uint32) { return 0xffff, 0xffff, 0xffff, 0xffff }

func TestMenuWithoutTerminal(t *testing.T) {
	u := NewWith(&bytes.Buffer{}, nil)
	if _, err := u.Menu("x", testRows(), 0); err != ErrNoTerminal {
		t.Errorf("got %v", err)
	}
}

// d hides the docs, and the commands then fill the screen, titles whole; d
// shows them again.
func TestMenuHidesAndShowsTheDocs(t *testing.T) {
	m := newTest(120, 30, Look{})
	long := "Add or change a host secret that has a long title"
	m.prefs.ListWidth = 28
	m.settle()
	lacks(t, screen(m), long)
	send(m, "d")
	lines := screen(m)
	has(t, lines, long)
	lacks(t, lines, "fleet host check [HOST]")
	has(t, lines, "d show docs")
	lacks(t, lines, "tab docs")
	if !m.prefs.DocsHidden {
		t.Error("hiding the docs is not kept")
	}
	send(m, "tab")
	if m.guideFocus {
		t.Error("tab moved into hidden docs")
	}
	send(m, "d")
	has(t, screen(m), "fleet host check [HOST]")
}

// < and > narrow and widen the commands panel, within what leaves the docs
// room; = fits it to the longest title.
func TestMenuResizesTheCommandsPanel(t *testing.T) {
	m := newTest(120, 30, Look{})
	start := m.geometry().left
	send(m, ">")
	if got := m.geometry().left; got != start+4 {
		t.Errorf("> made it %d, want %d", got, start+4)
	}
	send(m, "<", "<")
	if got := m.geometry().left; got != start-4 {
		t.Errorf("< < made it %d, want %d", got, start-4)
	}
	for i := 0; i < 40; i++ {
		send(m, "<")
	}
	if got := m.geometry().left; got != minListWidth {
		t.Errorf("narrowest is %d, want %d", got, minListWidth)
	}
	for i := 0; i < 40; i++ {
		send(m, ">")
	}
	if got := m.geometry(); got.right != minDocsWidth {
		t.Errorf("widest leaves the docs %d, want %d", got.right, minDocsWidth)
	}
	send(m, "=")
	has(t, screen(m), "Add or change a host secret that has a long title")
	for _, l := range screen(m) {
		if w := ansi.StringWidth(l); w > 120 {
			t.Errorf("a line is %d wide: %q", w, l)
		}
	}
}

// ? covers the screen with every key; any key goes back.
func TestMenuListsItsKeys(t *testing.T) {
	m := newTest(100, 30, Look{})
	send(m, "?")
	lines := screen(m)
	has(t, lines, "Keys")
	has(t, lines, "fit the commands panel to its longest title")
	has(t, lines, "any key: back")
	send(m, "x")
	lacks(t, screen(m), "any key: back")
	if m.done {
		t.Error("closing the keys did something else")
	}
}

// The key line fits, dropping the least needed keys first; ? and q stay.
func TestMenuKeyLineFits(t *testing.T) {
	for _, w := range []int{72, 80, 100, 140} {
		m := newTest(w, 30, Look{})
		last := screen(m)[29]
		if ansi.StringWidth(last) > w || !strings.Contains(last, "? keys") || !strings.Contains(last, "q quit") {
			t.Errorf("at %d: %q", w, last)
		}
	}
}

// The layout is kept between runs.
func TestMenuPrefsAreKept(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if p := LoadMenuPrefs(); p != (MenuPrefs{}) {
		t.Errorf("nothing kept yet, but %+v", p)
	}
	MenuPrefs{ListWidth: 52, DocsHidden: true}.Save()
	if p := LoadMenuPrefs(); p != (MenuPrefs{ListWidth: 52, DocsHidden: true}) {
		t.Errorf("read back %+v", p)
	}
	MenuPrefs{}.Save()
	if p := LoadMenuPrefs(); p != (MenuPrefs{}) {
		t.Errorf("reset read back %+v", p)
	}
}
