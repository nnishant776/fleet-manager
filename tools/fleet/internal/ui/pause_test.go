package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPause(t *testing.T) {
	for in, want := range map[string]bool{"\n": true, "q\n": false, "quit\n": false, " Q \n": false} {
		var out bytes.Buffer
		u := NewScripted(&out, strings.NewReader(in))
		back, err := u.Pause("Enter: back to the menu · q: quit")
		if err != nil || back != want {
			t.Errorf("%q: back %v, err %v", in, back, err)
		}
		if !strings.Contains(out.String(), "Enter: back to the menu") {
			t.Errorf("prompt not shown: %q", out.String())
		}
	}
	u := NewWith(&bytes.Buffer{}, nil)
	if _, err := u.Pause("x"); err != ErrNoTerminal {
		t.Errorf("got %v", err)
	}
}
