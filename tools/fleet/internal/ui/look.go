package ui

import (
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
// text. The console has no grey to spare as a background, so there it is
// reverse video, which shows on any colours.
func (l Look) Highlight(dark bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	switch {
	case l.Console:
		return s.Reverse(true)
	case dark:
		return s.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("231"))
	default:
		return s.Background(lipgloss.Color("253")).Foreground(lipgloss.Color("16"))
	}
}
