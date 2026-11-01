package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Pause asks for nothing but a decision to go on: an empty line is back; q or
// quit is not. Anything else is taken as back.
func (u *UI) Pause(prompt string) (back bool, err error) {
	if !u.tty {
		return false, ErrNoTerminal
	}
	_, _ = lipgloss.Fprint(u.out, u.dim.Render(prompt)+" ")
	line, err := u.readLine()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "q", "quit":
		return false, nil
	}
	return true, nil
}
