package ui

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestDetectLookTellsTheConsoleApart(t *testing.T) {
	for _, c := range []struct {
		vars    map[string]string
		console bool
	}{
		{map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}, false},
		{map[string]string{"TERM": "xterm-256color"}, false}, // no locale: UTF-8, as terminals are
		{map[string]string{"TERM": "linux", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"TERM": "vt220"}, true},
		{map[string]string{"TERM": "xterm", "LANG": "C"}, true},
		{map[string]string{"TERM": "xterm", "LC_ALL": "en_US.utf8", "LANG": "C"}, false}, // LC_ALL first
		{map[string]string{"TERM": "xterm", "LC_CTYPE": "POSIX", "LANG": "en_US.UTF-8"}, true},
	} {
		if got := DetectLook(env(c.vars)).Console; got != c.console {
			t.Errorf("%v: console = %v, want %v", c.vars, got, c.console)
		}
	}
}

func TestFleetThemeOverridesWhatTheTerminalSays(t *testing.T) {
	if l := DetectLook(env(map[string]string{"FLEET_THEME": "Light"})); l.Dark(true) {
		t.Error("FLEET_THEME=Light drew for a dark background")
	}
	if l := DetectLook(env(map[string]string{"FLEET_THEME": "dark"})); !l.Dark(false) {
		t.Error("FLEET_THEME=dark drew for a light background")
	}
	l := DetectLook(env(map[string]string{"FLEET_THEME": "solarized"}))
	if l.Dark(false) || !l.Dark(true) {
		t.Error("an unknown FLEET_THEME did not leave it to the terminal")
	}
}

// The highlighted row of a list has a grey bar behind it: light on a light
// terminal, dark on a dark one; reverse video on the console.
func TestTheHighlightedRowHasABar(t *testing.T) {
	u := NewWith(&bytes.Buffer{}, nil)
	u.look = Look{}
	light := u.theme(false).Focused.SelectedOption.Render("Check a host")
	dark := u.theme(true).Focused.SelectedOption.Render("Check a host")
	if !strings.Contains(light, "48;5;252") {
		t.Errorf("light: no light grey background in %q", light)
	}
	if !strings.Contains(dark, "48;5;238") {
		t.Errorf("dark: no dark grey background in %q", dark)
	}
	if lipgloss.Width(light) != len("Check a host")+2 {
		t.Errorf("the bar is not a space wider on each side: %q", light)
	}
	u.look = Look{Console: true}
	console := u.theme(true).Focused.SelectedOption.Render("Check a host")
	if !strings.Contains(console, "47") || !strings.Contains(console, "30") || strings.Contains(console, "48;5;") || strings.Contains(console, "\x1b[1") {
		t.Errorf("console: not black on the basic grey, or bold, or a 256-colour background: %q", console)
	}
	if got := u.theme(true).Focused.SelectSelector.String(); !strings.Contains(got, "> ") {
		t.Errorf("console selector %q", got)
	}
	u.look = Look{Theme: "light"}
	if forced := u.theme(true).Focused.SelectedOption.Render("x"); !strings.Contains(forced, "48;5;252") {
		t.Errorf("FLEET_THEME=light on a terminal that says dark: %q", forced)
	}
}

func TestListLabelsArePaddedToOneWidth(t *testing.T) {
	got := padded([]string{"a", "three", "üb"})
	for _, l := range got {
		if lipgloss.Width(l) != 5 {
			t.Errorf("%q is %d wide, want 5", l, lipgloss.Width(l))
		}
	}
	if strings.TrimRight(got[2], " ") != "üb" {
		t.Errorf("label changed: %q", got[2])
	}
}
