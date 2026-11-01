package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestTypedWantsTheWordExactly(t *testing.T) {
	var out bytes.Buffer
	u := NewScripted(&out, strings.NewReader("host/nas\nmain\nyes\n"))
	for i, want := range []bool{false, true} {
		got, err := u.Typed("Rewrite?", []string{"main", "main"}[i])
		if err != nil || got != want {
			t.Errorf("answer %d: %v, %v", i, got, err)
		}
	}
	if ok, err := u.Confirm("Go on?", false); err != nil || !ok {
		t.Errorf("Confirm after Typed: %v, %v", ok, err)
	}
	if _, err := NewWith(&out, nil).Typed("Rewrite?", "main"); err != ErrNoTerminal {
		t.Errorf("without a terminal: %v", err)
	}
}
