package flows

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// What fleet inventory import changes in the files it copies, as pure
// functions over the text of a .nix file. The scanner reads just enough Nix to
// tell a relative path literal (./x, ../x) and a string literal ("…") from the
// same characters in a comment or another string.

type nixTokKind int

const (
	tokPath nixTokKind = iota
	tokString
)

// nixTok is a path or string literal at src[start:end]; a string includes
// its quotes.
type nixTok struct {
	kind       nixTokKind
	start, end int
}

func isPathChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '+' || c == '/'
}

// scanNix finds the relative path literals and the plain string literals of
// src, in order, leaving out whatever is inside comments (# and /* */) and
// indented strings (”…”). A string with an escape or an interpolation is
// still returned: its user decides whether to read it.
func scanNix(src string) []nixTok {
	var toks []nixTok
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				i += 2 + j + 2
			} else {
				i = n
			}
		case c == '"':
			j := i + 1
			for j < n && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= n {
				return toks
			}
			toks = append(toks, nixTok{tokString, i, j + 1})
			i = j + 1
		case c == '\'' && i+1 < n && src[i+1] == '\'':
			j := i + 2
			for j < n {
				if strings.HasPrefix(src[j:], "'''") || strings.HasPrefix(src[j:], "''$") || strings.HasPrefix(src[j:], "''\\") {
					j += 3
					continue
				}
				if strings.HasPrefix(src[j:], "''") {
					break
				}
				j++
			}
			i = j + 2
		case c == '.' && (strings.HasPrefix(src[i:], "./") || strings.HasPrefix(src[i:], "../")):
			if i > 0 && (isPathChar(src[i-1]) || src[i-1] == '\'' || src[i-1] == '~' || src[i-1] == '$') {
				i++
				continue
			}
			j := i
			for j < n && isPathChar(src[j]) {
				j++
			}
			if src[j-1] != '/' {
				toks = append(toks, nixTok{tokPath, i, j})
			}
			i = j
		default:
			i++
		}
	}
	return toks
}

// findPathLiterals is the relative path literals of src, as written.
func findPathLiterals(src string) []string {
	var out []string
	for _, t := range scanNix(src) {
		if t.kind == tokPath {
			out = append(out, src[t.start:t.end])
		}
	}
	return out
}

type edit struct {
	start, end int
	text       string
}

func applyEdits(src string, edits []edit) string {
	sort.Slice(edits, func(a, b int) bool { return edits[a].start < edits[b].start })
	var b strings.Builder
	last := 0
	for _, e := range edits {
		b.WriteString(src[last:e.start])
		b.WriteString(e.text)
		last = e.end
	}
	b.WriteString(src[last:])
	return b.String()
}

// relocation is where the parts of fleet-manager's tree that a host takes
// along go in the inventory.
type relocation struct {
	Host     string
	HostsDir string          // the inventory's folder of hosts, such as config/hosts
	UsersDir string          // and of users; "" when it has none
	Users    map[string]bool // the accounts whose folders come along
}

const signersPath = "fleet/allowed_signers"

// unitOf is the folder that moves as a whole and holds p: the host's, or a
// user's that comes along. "" for anything else.
func (r relocation) unitOf(p string) string {
	if underPath(p, "hosts/"+r.Host) {
		return "hosts/" + r.Host
	}
	if rest, ok := strings.CutPrefix(p, "users/"); ok {
		u, _, _ := strings.Cut(rest, "/")
		if r.Users[u] {
			return "users/" + u
		}
	}
	return ""
}

func underPath(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// newPlace is the path in the inventory of a path in fleet-manager's tree
// that moves; ok is false for one that does not.
func (r relocation) newPlace(p string) (string, bool) {
	switch unit := r.unitOf(p); {
	case p == signersPath:
		return signersPath, true
	case strings.HasPrefix(unit, "hosts/"):
		return path.Join(r.HostsDir, r.Host) + strings.TrimPrefix(p, unit), true
	case strings.HasPrefix(unit, "users/") && r.UsersDir != "":
		return path.Join(r.UsersDir, strings.TrimPrefix(unit, "users/")) + strings.TrimPrefix(p, unit), true
	}
	return "", false
}

// relPathLiteral writes the path to, as seen from the folder from, as a Nix
// path literal.
func relPathLiteral(from, to string) string {
	rel, err := filepath.Rel(filepath.FromSlash(from), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

// rewritePathLiterals returns the text of the file at oldFile (a path in
// fleet-manager's tree) with each relative path literal that leaves the
// folder it moves in made to reach the same file from where the file goes in
// the inventory. Literals that stay inside the folder are left as they are.
// The ones that reach anything else are returned, as they are written, in
// outside; the text keeps them.
func rewritePathLiterals(src, oldFile string, r relocation) (out string, outside []string) {
	newFile, ok := r.newPlace(oldFile)
	if !ok {
		return src, nil
	}
	oldDir, newDir := path.Dir(oldFile), path.Dir(newFile)
	var edits []edit
	for _, t := range scanNix(src) {
		if t.kind != tokPath {
			continue
		}
		lit := src[t.start:t.end]
		target := path.Join(oldDir, lit)
		if unit := r.unitOf(oldFile); unit != "" && unit == r.unitOf(target) {
			continue
		}
		to, ok := r.newPlace(target)
		if !ok {
			outside = append(outside, lit)
			continue
		}
		if now := relPathLiteral(newDir, to); now != lit {
			edits = append(edits, edit{t.start, t.end, now})
		}
	}
	return applyEdits(src, edits), outside
}

// replaceAddress changes each string literal that holds an address of the
// repository with id fmID (however it is written) to newAddr, and says how
// many it changed.
func replaceAddress(src, fmID, newAddr string) (string, int) {
	var edits []edit
	for _, t := range scanNix(src) {
		if t.kind != tokString {
			continue
		}
		content := src[t.start+1 : t.end-1]
		if strings.ContainsAny(content, "\\$") || content == newAddr {
			continue
		}
		if id, err := fleet.RepoID(content); err == nil && id == fmID {
			edits = append(edits, edit{t.start + 1, t.end - 1, newAddr})
		}
	}
	return applyEdits(src, edits), len(edits)
}

// addressFromOrigin is the address hosts fetch an inventory from, when its
// origin is where it is pushed to: https://H/O/R(.git) is git@H:O/R.git, and
// ssh://… and git@H:… are as they are.
func addressFromOrigin(origin string) (string, error) {
	give := fmt.Errorf("%s is not an address hosts can fetch from: give --address, the address hosts fetch the inventory from", origin)
	switch {
	case strings.HasPrefix(origin, "ssh://"):
		return origin, nil
	case strings.HasPrefix(origin, "https://"), strings.HasPrefix(origin, "http://"):
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" {
			return "", give
		}
		p := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
		if strings.Count(p, "/") < 1 {
			return "", give
		}
		return "git@" + u.Hostname() + ":" + p + ".git", nil
	case !strings.Contains(origin, "://") && strings.Contains(origin, "@") && strings.Contains(origin, ":"):
		return origin, nil
	}
	return "", give
}
