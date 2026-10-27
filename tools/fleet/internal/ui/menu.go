package ui

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// MenuRow is one choice of a Menu.
type MenuRow struct {
	Section string // set on the first row of its section
	Label   string // the command's title
	Path    string // the command, such as "host check"; the filter matches it too
	Guide   Guide  // shown beside the list while the row is highlighted
}

// Menu is a full-screen choice of commands: the list on the left, the
// highlighted command's guide on the right. It returns the index of the
// chosen row, or ErrCancelled. start is the row to highlight first. The
// screen is restored when it returns, so the command then runs in the normal
// scrollback.
func (u *UI) Menu(title string, rows []MenuRow, start int) (int, error) {
	if !u.tty || u.in == nil {
		return 0, ErrNoTerminal
	}
	m := newMenu(title, rows, start, u.look)
	p := tea.NewProgram(m, tea.WithContext(u.ctx), tea.WithInput(u.in), tea.WithOutput(u.out))
	if _, err := p.Run(); err != nil {
		if u.ctx.Err() != nil || errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
			return 0, ErrCancelled
		}
		return 0, err
	}
	return m.result()
}

// The model of the menu (a bubbletea model). Everything it draws is a pure
// function of its state and the terminal's size, so tests drive it with
// messages and read View.
type menuModel struct {
	title string
	rows  []MenuRow
	look  Look

	answeredDark bool // what the terminal said its background is; dark until it does
	width        int
	height       int

	cursor     int // index into rows; -1 when the filter leaves nothing
	listTop    int // first entry shown in the list
	scroll     int // first line shown of the guide
	guideFocus bool
	filtering  bool
	filter     string

	done      bool
	cancelled bool
}

func newMenu(title string, rows []MenuRow, start int, look Look) *menuModel {
	m := &menuModel{title: title, rows: rows, look: look, answeredDark: true}
	if start >= 0 && start < len(rows) {
		m.cursor = start
	}
	if len(rows) == 0 {
		m.cursor = -1
	}
	return m
}

func (m *menuModel) result() (int, error) {
	if m.done && !m.cancelled && m.cursor >= 0 {
		return m.cursor, nil
	}
	return 0, ErrCancelled
}

// Init asks the terminal for its background colour.
func (m *menuModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *menuModel) dark() bool { return m.look.Dark(m.answeredDark) }

// entry is one line of the list: a section's name (row -1) or a command.
type entry struct {
	section string
	row     int
}

// matches reports whether the filter lets row i through.
func (m *menuModel) matches(i int) bool {
	if m.filter == "" {
		return true
	}
	f := strings.ToLower(m.filter)
	return strings.Contains(strings.ToLower(m.rows[i].Label), f) ||
		strings.Contains(strings.ToLower(m.rows[i].Path), f)
}

// entries is the list as shown: the commands that match, each section's name
// before its first one.
func (m *menuModel) entries() []entry {
	var out []entry
	section, shown := "", true
	for i, r := range m.rows {
		if r.Section != "" {
			section, shown = r.Section, false
		}
		if !m.matches(i) {
			continue
		}
		if !shown {
			out = append(out, entry{section: section, row: -1})
			shown = true
		}
		out = append(out, entry{row: i})
	}
	return out
}

// commands are the rows the cursor can be on, in order.
func (m *menuModel) commands() []int {
	var out []int
	for _, e := range m.entries() {
		if e.row >= 0 {
			out = append(out, e.row)
		}
	}
	return out
}

type geometry struct {
	wide   bool
	left   int // outer widths of the panels
	right  int
	height int // outer height of a panel
}

func (m *menuModel) geometry() geometry {
	g := geometry{wide: m.width >= 72 && m.height >= 16, height: max(m.height-2, 3)}
	if !g.wide {
		g.left, g.right = m.width, m.width
		return g
	}
	longest := 0
	for _, r := range m.rows {
		longest = max(longest, lipgloss.Width(r.Label))
	}
	g.left = min(max(longest+6, 28), 44, m.width*40/100)
	g.right = m.width - g.left
	return g
}

func (g geometry) inner() int { return max(g.height-2, 1) }

// guideWidth is the width the guide is wrapped to.
func (m *menuModel) guideWidth() int {
	g := m.geometry()
	if g.wide {
		return max(g.right-2, 1)
	}
	return max(m.width-2, 1)
}

func (m *menuModel) setCursor(i int) {
	if i != m.cursor {
		m.scroll = 0
	}
	m.cursor = i
}

func (m *menuModel) move(delta int) {
	cmds := m.commands()
	if len(cmds) == 0 {
		return
	}
	pos := -1
	for p, r := range cmds {
		if r == m.cursor {
			pos = p
		}
	}
	if pos < 0 {
		m.setCursor(cmds[0])
		return
	}
	m.setCursor(cmds[min(max(pos+delta, 0), len(cmds)-1)])
}

// settle keeps the cursor on a row that is shown and the list scrolled to it.
func (m *menuModel) settle() {
	cmds := m.commands()
	found := false
	for _, r := range cmds {
		found = found || r == m.cursor
	}
	if !found {
		if len(cmds) > 0 {
			m.setCursor(cmds[0])
		} else {
			m.setCursor(-1)
		}
	}
	entries := m.entries()
	ci := -1
	for i, e := range entries {
		if e.row == m.cursor && e.row >= 0 {
			ci = i
		}
	}
	ih := m.geometry().inner()
	if ci >= 0 {
		if ci < m.listTop {
			m.listTop = ci
		}
		if ci >= m.listTop+ih {
			m.listTop = ci - ih + 1
		}
		// The name of the section stays with its first command.
		if ci > 0 && entries[ci-1].row < 0 && m.listTop > ci-1 {
			m.listTop = ci - 1
		}
	}
	m.listTop = min(max(m.listTop, 0), max(len(entries)-ih, 0))
	m.scroll = min(max(m.scroll, 0), m.maxScroll())
}

func (m *menuModel) maxScroll() int {
	if m.cursor < 0 {
		return 0
	}
	return max(len(m.guideLines(m.rows[m.cursor].Guide, m.guideWidth()))-m.geometry().inner(), 0)
}

func (m *menuModel) scrollBy(d int) { m.scroll = min(max(m.scroll+d, 0), m.maxScroll()) }

// Update handles a message.
func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.answeredDark = msg.IsDark()
	case tea.KeyPressMsg:
		if cmd := m.key(msg); cmd != nil {
			return m, cmd
		}
	}
	m.settle()
	return m, nil
}

func (m *menuModel) key(msg tea.KeyPressMsg) tea.Cmd {
	half := max(m.geometry().inner()/2, 1)
	k := msg.String()
	switch k {
	case "ctrl+c":
		m.cancelled, m.done = true, true
		return tea.Quit
	case "esc":
		if m.filtering {
			m.filtering, m.filter = false, ""
			return nil
		}
		m.cancelled, m.done = true, true
		return tea.Quit
	case "enter":
		if m.cursor >= 0 {
			m.done = true
			return tea.Quit
		}
	case "tab":
		m.guideFocus = !m.guideFocus
	case "up":
		m.step(-1)
	case "down":
		m.step(1)
	case "home":
		if m.guideFocus {
			m.scroll = 0
		} else if cmds := m.commands(); len(cmds) > 0 {
			m.setCursor(cmds[0])
		}
	case "end":
		if m.guideFocus {
			m.scroll = m.maxScroll()
		} else if cmds := m.commands(); len(cmds) > 0 {
			m.setCursor(cmds[len(cmds)-1])
		}
	case "pgup", "ctrl+u":
		m.scrollBy(-half)
	case "pgdown", "ctrl+d":
		m.scrollBy(half)
	case "backspace":
		if m.filtering {
			if m.filter == "" {
				m.filtering = false
			} else {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
			}
		}
	default:
		if m.filtering {
			if msg.Key().Text != "" && msg.Key().Mod == 0 {
				m.filter += msg.Key().Text
			}
			return nil
		}
		switch k {
		case "q":
			m.cancelled, m.done = true, true
			return tea.Quit
		case "/":
			m.filtering = true
		case "k":
			m.step(-1)
		case "j":
			m.step(1)
		}
	}
	return nil
}

// step is up or down: through the list, or through the guide when it has the
// focus.
func (m *menuModel) step(d int) {
	if m.guideFocus {
		m.scrollBy(d)
		return
	}
	m.move(d)
}

// ---- drawing ----

// View draws the whole screen.
func (m *menuModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *menuModel) ascii(unicode, plain string) string {
	if m.look.Console {
		return plain
	}
	return unicode
}

func (m *menuModel) cut(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, m.look.Glyphs().Ellipsis)
}

func padRight(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func (m *menuModel) render() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(Dim)
	g := m.geometry()

	var lines []string
	head := " fleet " + m.ascii("·", "-") + " " + m.title
	lines = append(lines, bold.Render(padRight(ansi.Truncate(head, m.width, ""), m.width)))

	showList := g.wide || !m.guideFocus
	showGuide := g.wide || m.guideFocus
	var list, guide []string
	if showList {
		list = m.panel("Commands", m.listBody(g), g.left, g.height, !m.guideFocus)
	}
	if showGuide {
		title, body := "", []string(nil)
		if m.cursor >= 0 {
			title = m.rows[m.cursor].Label
			body = m.guideBody(m.guideLines(m.rows[m.cursor].Guide, m.guideWidth()), g.inner())
		}
		guide = m.panel(title, body, g.right, g.height, m.guideFocus)
	}
	switch {
	case g.wide:
		for i := range list {
			lines = append(lines, list[i]+guide[i])
		}
	case m.guideFocus:
		lines = append(lines, guide...)
	default:
		lines = append(lines, list...)
	}
	lines = append(lines, dim.Render(padRight(m.keyLine(), m.width)))

	for i, l := range lines {
		if ansi.StringWidth(l) > m.width {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:m.height], "\n")
}

func (m *menuModel) keyLine() string {
	sep := " " + m.ascii("·", "|") + " "
	if m.filtering {
		return " filter: " + m.filter + m.ascii("▏", "_") + sep + "esc clear"
	}
	updown := m.ascii("↑↓", "up/down")
	full := []string{updown + " move", "enter run", "/ filter", "tab guide", "pgup/pgdn scroll", "q quit"}
	short := []string{updown + " move", "enter run", "tab guide", "q quit"}
	line := " " + strings.Join(full, sep)
	if ansi.StringWidth(line) > m.width {
		line = " " + strings.Join(short, sep)
	}
	return line
}

// panel draws a box with title in its top border; body lines are cut or
// padded to the inside.
func (m *menuModel) panel(title string, body []string, w, h int, focused bool) []string {
	b := m.look.Glyphs().Border
	var colour color.Color = Dim
	if focused {
		colour = m.look.Accent()
	}
	edge := lipgloss.NewStyle().Foreground(colour)
	iw, ih := max(w-2, 0), max(h-2, 0)

	title = m.cut(title, max(w-4, 0))
	var top string
	if title == "" {
		top = edge.Render(b.TopLeft + strings.Repeat(b.Top, iw) + b.TopRight)
	} else {
		fill := max(w-4-ansi.StringWidth(title), 0)
		top = edge.Render(b.TopLeft) + " " + lipgloss.NewStyle().Bold(true).Render(title) + " " +
			edge.Render(strings.Repeat(b.Top, fill)+b.TopRight)
	}
	out := []string{top}
	for i := 0; i < ih; i++ {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		if ansi.StringWidth(l) > iw {
			l = ansi.Truncate(l, iw, "")
		}
		out = append(out, edge.Render(b.Left)+padRight(l, iw)+edge.Render(b.Right))
	}
	out = append(out, edge.Render(b.BottomLeft+strings.Repeat(b.Bottom, iw)+b.BottomRight))
	return out
}

func (m *menuModel) listBody(g geometry) []string {
	iw := max(g.left-2, 1)
	entries := m.entries()
	if len(entries) == 0 {
		return []string{lipgloss.NewStyle().Foreground(Dim).Render("  no match")}
	}
	section := m.look.Section(m.dark())
	var out []string
	for _, e := range entries[min(m.listTop, len(entries)):] {
		if len(out) >= g.inner() {
			break
		}
		if e.row < 0 {
			out = append(out, section.Render(m.cut(" "+e.section, iw)))
			continue
		}
		label := m.cut(m.rows[e.row].Label, max(iw-2, 1))
		if e.row == m.cursor {
			out = append(out, m.look.Highlight(m.dark()).Render(padRight(m.look.Selector()+label, iw)))
		} else {
			out = append(out, "  "+label)
		}
	}
	return out
}

// guideBody is the part of the guide in view, with a marker on the first or
// last line when there is more above or below.
func (m *menuModel) guideBody(lines []string, ih int) []string {
	n := len(lines)
	if n <= ih {
		return lines
	}
	dim := lipgloss.NewStyle().Foreground(Dim)
	s := min(max(m.scroll, 0), n-ih)
	out := append([]string(nil), lines[s:s+ih]...)
	if s > 0 {
		out[0] = dim.Render(m.ascii("↑", "^") + " (pgup)")
	}
	if s+ih < n {
		out[ih-1] = dim.Render(m.ascii("↓", "v") + " more (pgdn)")
	}
	return out
}

// guideLines lays the guide out in lines of at most w columns.
func (m *menuModel) guideLines(g Guide, w int) []string {
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(Dim)
	var blocks [][]string
	styled := func(style lipgloss.Style, lines []string) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = style.Render(l)
		}
		return out
	}
	hang := func(first, rest, text string, width int) []string {
		var out []string
		for i, l := range wrap(text, max(width-len(first), 1)) {
			if i == 0 {
				out = append(out, first+l)
			} else {
				out = append(out, rest+l)
			}
		}
		return out
	}

	if g.Usage != "" {
		blocks = append(blocks, styled(lipgloss.NewStyle().Bold(true).Foreground(m.look.Accent()), wrap(g.Usage, w)))
	}
	if len(g.Examples) > 0 {
		// An example too long for the panel goes on, indented, on the next lines.
		var examples []string
		for _, e := range g.Examples {
			examples = append(examples, hang("  ", "      ", strings.TrimSpace(e), w)...)
		}
		blocks = append(blocks, styled(dim, examples))
	}
	var about []string
	for _, p := range g.About {
		if len(about) > 0 {
			about = append(about, "")
		}
		about = append(about, wrap(strings.Join(strings.Fields(p), " "), w)...)
	}
	if len(about) > 0 {
		blocks = append(blocks, about)
	}
	if len(g.Steps) > 0 {
		steps := []string{bold.Render("Steps")}
		digits := len(strconv.Itoa(len(g.Steps)))
		for i, s := range g.Steps {
			lead := fmt.Sprintf(" %*d ", digits, i+1)
			rest := strings.Repeat(" ", len(lead))
			steps = append(steps, hang(lead, rest, s.Name, w)...)
			if s.Text != "" {
				steps = append(steps, styled(dim, hang(rest, rest, s.Text, w))...)
			}
		}
		steps = append(steps, styled(dim, wrap("--dry-run shows every command a step runs.", w))...)
		blocks = append(blocks, steps)
	}
	if len(g.Flags) > 0 {
		flags := []string{bold.Render("Flags")}
		for _, f := range g.Flags {
			flags = append(flags, bold.Render(f.Name))
			flags = append(flags, hang("  ", "  ", f.Text, w)...)
		}
		blocks = append(blocks, flags)
	}
	if len(g.Needs) > 0 {
		blocks = append(blocks, styled(dim, wrap("Needs: "+strings.Join(g.Needs, ", "), w)))
	}

	var lines []string
	for i, b := range blocks {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, b...)
	}
	return lines
}
