package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MenuPrefs are how the menu was last laid out: the commands panel's width,
// and whether the docs panel is hidden. They are kept between runs in
// $XDG_STATE_HOME/fleet/menu (~/.local/state/fleet/menu), a line each.
type MenuPrefs struct {
	ListWidth  int  // the commands panel's outer width; 0 sizes it by itself
	DocsHidden bool // the docs panel is hidden, and the commands fill the screen
}

func menuPrefsPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "fleet", "menu")
}

// LoadMenuPrefs reads the kept layout; anything missing or unreadable is the
// default.
func LoadMenuPrefs() MenuPrefs {
	var p MenuPrefs
	path := menuPrefsPath()
	if path == "" {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "list-width":
			if n, err := strconv.Atoi(f[1]); err == nil && n > 0 {
				p.ListWidth = n
			}
		case "docs":
			p.DocsHidden = f[1] == "hidden"
		}
	}
	return p
}

// Save keeps the layout for the next run. A layout that cannot be kept is
// no reason to fail, so errors are dropped.
func (p MenuPrefs) Save() {
	path := menuPrefsPath()
	if path == "" {
		return
	}
	docs := "shown"
	if p.DocsHidden {
		docs = "hidden"
	}
	data := "list-width " + strconv.Itoa(p.ListWidth) + "\ndocs " + docs + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(data), 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}
