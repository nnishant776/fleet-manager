package fleet

// Phase 2 of fleet trust, part C: the tools' own clones. The tools keep a
// clone of each repository they work on, under
// $XDG_DATA_HOME/fleet/repos/<id> (~/.local/share/fleet/repos/<id> when it is
// unset or not absolute, on macOS too), where <id> is RepoID of its address.
// Nobody edits it: a command fetches it, takes main's newest commit only
// when a key you trust signed it and it follows the commit you trusted last,
// and leaves the clone detached there. A name for each clone, so that
// --flake inventory means it, is kept in $XDG_STATE_HOME/fleet/names, one
// line per name: the name, a space, the id.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// DataDir is $XDG_DATA_HOME/fleet, or ~/.local/share/fleet when it is unset
// or not absolute, on macOS as on Linux.
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "fleet"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "fleet"), nil
}

// ClonePath is where the tools' clone of the repository at address lives.
func ClonePath(address string) (string, error) {
	id, err := RepoID(address)
	if err != nil {
		return "", err
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repos", filepath.FromSlash(id)), nil
}

// NamePattern is what a repository's name looks like.
const NamePattern = `^[a-z0-9][a-z0-9._-]*$`

var nameRe = regexp.MustCompile(NamePattern)

func namesPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "names"), nil
}

// ReadNames is the names of the clones, name to id. No file is no names.
func ReadNames() (map[string]string, error) {
	p, err := namesPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || !nameRe.MatchString(f[0]) {
			return nil, fmt.Errorf("%s line %d is not a name and a repository: %q", p, n+1, line)
		}
		names[f[0]] = f[1]
	}
	return names, nil
}

// SetName names the repository with the given id. A name that names another
// repository is refused; the id's previous name, if it had one, is replaced.
func SetName(name, id string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%q is not a name: use lowercase letters, digits, . _ -, starting with a letter or digit", name)
	}
	names, err := ReadNames()
	if err != nil {
		return err
	}
	if old, ok := names[name]; ok && old != id {
		return fmt.Errorf("%s already names %s", name, old)
	}
	for n, i := range names {
		if i == id {
			delete(names, n)
		}
	}
	names[name] = id
	p, err := namesPath()
	if err != nil {
		return err
	}
	var keys []string
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, n := range keys {
		b.WriteString(n + " " + names[n] + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// NameOf is the name of the repository with the id, or "".
func NameOf(id string) string {
	names, err := ReadNames()
	if err != nil {
		return ""
	}
	for n, i := range names {
		if i == id {
			return n
		}
	}
	return ""
}

// ResolveName turns a registered name into the path of the tools' clone of
// that repository, and leaves a path or an address as it is. For a name it
// also sets <input>_name, which says the value was a name. It looks nothing
// up on the network.
func ResolveName(x *engine.Ctx, input, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "/:") || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "~") {
		return value, nil
	}
	if _, err := os.Stat(value); err == nil {
		return value, nil
	}
	names, err := ReadNames()
	if err != nil {
		return "", err
	}
	id, ok := names[value]
	if !ok {
		return "", fmt.Errorf("%q is neither a path nor the name of a repository: fleet repo list shows the names, fleet repo add makes one", value)
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	x.Values[input+"_name"] = value
	return filepath.Join(dir, "repos", filepath.FromSlash(id)), nil
}

// SyncClone brings the tools' clone in dir up to date, and stops at the
// first thing that is not right: it fetches, checks that origin's main is
// signed by a key you trust (the clone's own gpg.ssh.allowedSignersFile,
// which fleet repo add sets), checks that it follows the commit you trusted
// last, and detaches the clone there. Local branches that origin also has are
// moved forward when they only lag.
func SyncClone(x *engine.Ctx, dir string) error {
	if _, err := engine.Cmd("git", "-C", dir, "fetch", "-q", "--prune", "origin").
		ReadOnly().Quiet().OK("fetched origin into " + dir).Run(x); err != nil {
		return err
	}
	if _, err := engine.Cmd("git", "-C", dir, "verify-commit", "refs/remotes/origin/main").
		ReadOnly().Quiet().
		Explain(func(engine.Result) string {
			return "origin's main is not signed by a key you trust: git checks it against the clone's gpg.ssh.allowedSignersFile setting. " +
				"Look at it with git -C " + dir + " log --show-signature -1 origin/main"
		}).OK("origin's main is signed by a key you trust").Run(x); err != nil {
		return err
	}
	origin, err := OriginOf(x, dir)
	if err != nil {
		return err
	}
	if err := CheckTrusted(x, dir, origin, "main", "refs/remotes/origin/main"); err != nil {
		return err
	}
	tip, err := commitOf(x, dir, "refs/remotes/origin/main")
	if err != nil {
		return err
	}
	here, _ := commitOf(x, dir, "HEAD")
	detached := false
	if res, err := gitIn(x, dir, "symbolic-ref", "-q", "HEAD"); err != nil && res.Code != 0 {
		detached = true
	}
	switch {
	case here == tip && detached:
		x.UI.OK("the clone is at " + short(tip))
	case x.Options.DryRun:
		x.UI.Skip("dry run: the clone would move to " + short(tip))
	default:
		if _, err := engine.Cmd("git", "-C", dir, "checkout", "-q", "--detach", tip).
			ReadOnly().Quiet().OK("the clone is at " + short(tip)).Run(x); err != nil {
			return err
		}
	}
	return syncBranches(x, dir)
}

// syncBranches moves the clone's local branches that only lag behind
// origin's, and says what is left for a person to look at.
func syncBranches(x *engine.Ctx, dir string) error {
	res, err := gitIn(x, dir, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		b, local := f[0], f[1]
		remote, err := commitOf(x, dir, "refs/remotes/origin/"+b)
		if err != nil {
			continue // origin lacks it: it stays
		}
		if local == remote {
			continue
		}
		behind, err := follows(x, dir, local, remote)
		if err != nil {
			return err
		}
		if behind {
			if x.Options.DryRun {
				x.UI.Skip("dry run: " + b + " would move to origin's " + short(remote))
				continue
			}
			if _, err := engine.Cmd("git", "-C", dir, "update-ref", "refs/heads/"+b, remote, local).
				ReadOnly().Quiet().OK("moved " + b + " to origin's " + short(remote)).Run(x); err != nil {
				return err
			}
			continue
		}
		ahead, err := follows(x, dir, remote, local)
		if err != nil {
			return err
		}
		if ahead {
			x.UI.Note(b + " has commits not pushed: git -C " + dir + " push origin " + b)
			continue
		}
		x.UI.Note(b + " and origin's have gone apart: look at git -C " + dir + " log --oneline --graph " + b + " origin/" + b)
	}
	return nil
}
