package secrets

import (
	"regexp"
	"strings"
	"testing"
)

func TestKeyPathNestsOnSlash(t *testing.T) {
	for name, want := range map[string]string{
		"wifi_psk":                `["wifi_psk"]`,
		"hotspot/wls2ccf67579ff5": `["hotspot"]["wls2ccf67579ff5"]`,
		"wireguard/wg0/private":   `["wireguard"]["wg0"]["private"]`,
	} {
		if got := KeyPath(name); got != want {
			t.Errorf("KeyPath(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestKeyPatternTakesPartsSeparatedBySlash(t *testing.T) {
	re := regexp.MustCompile(KeyPattern)
	for _, ok := range []string{"wifi_psk", "hotspot-wls0", "hotspot/wls0", "a/b/c", "A1/b_2"} {
		if !re.MatchString(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "/a", "a/", "a//b", "a/../b", "-a", "a/-b", "a b", `a"b`, "a]b"} {
		if re.MatchString(bad) {
			t.Errorf("%q taken", bad)
		}
	}
}

func TestCheckPlace(t *testing.T) {
	doc := map[string]any{
		"wifi_psk": "ENC[…]",
		"hotspot":  map[string]any{"wls1": "ENC[…]", "wls0": "ENC[…]"},
		"sops":     map[string]any{"version": "3.13.3"},
	}
	for name, refused := range map[string]string{
		"new":                "",
		"wifi_psk":           "", // replaced
		"hotspot/wls0":       "", // replaced
		"hotspot/wls2":       "", // added to the group
		"wireguard/wg0/key":  "", // a new group
		"hotspot":            "hotspot holds secrets of its own (wls0, wls1), which one value would replace; name one of them, as hotspot/wls0",
		"wifi_psk/home":      "wifi_psk holds a value, so nothing can go under it",
		"hotspot/wls0/inner": "hotspot/wls0 holds a value",
		"sops":               `"sops" is where sops keeps the file's own keys`,
		"sops/version":       `"sops" is where sops keeps`,
		"users":              `"users" is where the host's file lists the users`,
		"users/alice":        `"users" is where the host's file lists the users`,
	} {
		err := CheckPlace(doc, name)
		switch {
		case refused == "" && err != nil:
			t.Errorf("%q refused: %v", name, err)
		case refused != "" && (err == nil || !strings.Contains(err.Error(), refused)):
			t.Errorf("%q: got %v, want it refused with %q", name, err, refused)
		}
	}
	if err := CheckPlace(nil, "hotspot/wls0"); err != nil {
		t.Errorf("an empty file refused a name: %v", err)
	}
}
