package ui

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// Look is what the terminal can show, found once at the start. A desktop
// terminal, or one reached over SSH, gets greys and Unicode. The Linux
// console, which the NixOS installer runs on, has sixteen colours, eight of
// them as backgrounds, and a small font: there fleet keeps to the basic
// colours and to glyphs that font has. Output that is not a terminal, or
// NO_COLOR, stays plain (lipgloss drops the colour by itself).
type Look struct {
	// Console is the Linux console (TERM=linux or vt*), or a locale that is
	// not UTF-8.
	Console bool
	// Theme is "light" or "dark" when FLEET_THEME says so; otherwise the
	// terminal is asked for its background.
	Theme string
}

// DetectLook reads the environment through getenv (os.Getenv by default).
func DetectLook(getenv func(string) string) Look {
	if getenv == nil {
		getenv = os.Getenv
	}
	var l Look
	term := getenv("TERM")
	if term == "linux" || strings.HasPrefix(term, "vt") {
		l.Console = true
	}
	// The locale that decides the character set: LC_ALL, then LC_CTYPE, then
	// LANG. None set is taken as UTF-8, as terminals are today.
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(name); v != "" {
			v = strings.ToLower(v)
			if !strings.Contains(v, "utf-8") && !strings.Contains(v, "utf8") {
				l.Console = true
			}
			break
		}
	}
	switch t := strings.ToLower(getenv("FLEET_THEME")); t {
	case "light", "dark":
		l.Theme = t
	}
	return l
}

// Dark says whether to draw for a dark background: FLEET_THEME if it is set,
// else what the terminal answered (dark when it did not answer, as the
// console is).
func (l Look) Dark(answered bool) bool {
	switch l.Theme {
	case "light":
		return false
	case "dark":
		return true
	}
	return answered
}

// Selector marks the highlighted row of a list.
func (l Look) Selector() string {
	if l.Console {
		return "> "
	}
	return "› "
}

// Highlight is the bar across the highlighted row of a list: a grey
// background, light on a light terminal and dark on a dark one, with bold
// text. The console has one grey among its eight backgrounds, so there it is
// black on that grey, and not bold: the console shows bold as a brighter
// colour, which would turn the black text grey.
func (l Look) Highlight(dark bool) lipgloss.Style {
	if l.Console {
		return lipgloss.NewStyle().Background(lipgloss.White).Foreground(lipgloss.Black)
	}
	s := lipgloss.NewStyle().Bold(true)
	switch {
	case dark:
		return s.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("231"))
	default:
		return s.Background(lipgloss.Color("252")).Foreground(lipgloss.Color("16"))
	}
}

// Accent is the colour of what leads the eye: the usage line, the focused
// panel's border. The console's blue is too dark on its black, so there it
// is cyan.
func (l Look) Accent() color.Color {
	if l.Console {
		return lipgloss.Cyan
	}
	return Accent
}

// Section is the style of a section's name in a list: yellow on a dark
// background, where it stands out, and the accent on a light one, where
// yellow fades into the white.
func (l Look) Section(dark bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if dark || l.Console {
		return s.Foreground(Warn)
	}
	return s.Foreground(l.Accent())
}

// Glyphs are the characters the output draws with. The console's font has
// √ × • · ─ │ ┌ ┐ └ ┘ but not ✓ ✗ … or rounded corners, so there fleet uses
// those it has.
type Glyphs struct {
	Ok, Fail, Skip, Note, Gutter, Ellipsis string
	Spinner                                []string
	Border                                 lipgloss.Border
}

// Glyphs returns the glyphs for this terminal.
func (l Look) Glyphs() Glyphs {
	if l.Console {
		return Glyphs{
			Ok: "√", Fail: "×", Skip: "-", Note: "!", Gutter: "│", Ellipsis: "...",
			Spinner: []string{"|", "/", "-", "\\"},
			Border:  lipgloss.NormalBorder(),
		}
	}
	return Glyphs{
		Ok: "✓", Fail: "✗", Skip: "–", Note: "!", Gutter: "│", Ellipsis: "…",
		Spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Border:  lipgloss.RoundedBorder(),
	}
}
