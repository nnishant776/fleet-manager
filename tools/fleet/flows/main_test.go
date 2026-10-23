package flows

import (
	"os"
	"testing"
)

// Every test gets a state folder of its own, so none writes to the real
// ~/.local/state; tests that need one set XDG_STATE_HOME themselves.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fleet-flows-state-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
