package secrets

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// KeyPattern is what a secret's name in a host's file may be: parts of
// letters, digits, - and _, with / between them to nest them, as
// hotspot/wls2ccf67579ff5. sops-nix reads a / in a secret's name the same
// way, and decrypts it to /run/secrets/hotspot/wls2ccf67579ff5.
const KeyPattern = `^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9][A-Za-z0-9_-]*)*$`

// KeyPath is a name's place in a sops file, as sops set and --extract take
// it: hotspot/wls2ccf67579ff5 is ["hotspot"]["wls2ccf67579ff5"].
func KeyPath(name string) string {
	var b strings.Builder
	for _, part := range strings.Split(name, "/") {
		b.WriteString(`["` + part + `"]`)
	}
	return b.String()
}

// SetAt is Set at a path from KeyPath, templated.
func SetAt(file, path, value, note string) *engine.Command {
	return cmd("sops", "set", "--value-stdin", file, path).StdinFrom(value).StdinNote(note)
}

// CheckPlace says why name cannot be stored in a file shaped like doc, or
// nil if it can. doc is the file as JSON: sops encrypts the values only, so
// its keys and nesting can be read without decrypting anything. A value
// is replaced; what is refused would lose secrets or break the file.
func CheckPlace(doc map[string]any, name string) error {
	parts := strings.Split(name, "/")
	if parts[0] == "users" {
		return fmt.Errorf("%q is where the host's file lists the users who keep their files on a branch of their own (fleet secrets user-enroll); pick another name", "users")
	}
	if parts[0] == "sops" {
		return fmt.Errorf("%q is where sops keeps the file's own keys; pick another name", "sops")
	}
	node := doc
	for i, part := range parts {
		v, ok := node[part]
		if !ok {
			return nil
		}
		here := strings.Join(parts[:i+1], "/")
		inner, isGroup := v.(map[string]any)
		if i == len(parts)-1 {
			if isGroup {
				var names []string
				for k := range inner {
					names = append(names, k)
				}
				slices.Sort(names)
				return fmt.Errorf("%s holds secrets of its own (%s), which one value would replace; name one of them, as %s/%s",
					here, strings.Join(names, ", "), here, names[0])
			}
			return nil
		}
		if !isGroup {
			return fmt.Errorf("%s holds a value, so nothing can go under it; pick another name, or replace %s itself", here, here)
		}
		node = inner
	}
	return nil
}
