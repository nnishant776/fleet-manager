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
	// Nor to the real clones in ~/.local/share/fleet: tests that need one set
	// XDG_DATA_HOME themselves.
	data, err := os.MkdirTemp("", "fleet-flows-data-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_DATA_HOME", data)
	// Tests stand in for the installer; one checks the refusal.
	onInstaller = func() bool { return true }
	code := m.Run()
	os.RemoveAll(dir)
	os.RemoveAll(data)
	os.Exit(code)
}
