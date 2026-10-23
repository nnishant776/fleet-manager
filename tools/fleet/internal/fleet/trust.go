package fleet

// The keys the maintainer trusts, phase 1 of fleet trust: a copy of each
// repository's fleet/allowed_signers, kept outside every checkout, which the
// tools check commits against. A checkout's own file cannot be the judge: a
// pushed commit that adds a key, signed with that key, would approve itself
// once pulled. Hosts are safe from this because they check against the file
// of the system they already run; this gives the maintainer's machine the
// same footing. Phase 2 keeps each repository's trusted commit beside it.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StateDir is $XDG_STATE_HOME/fleet, or ~/.local/state/fleet when it is
// unset, on macOS as on Linux.
func StateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "fleet"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "fleet"), nil
}

var scpLike = regexp.MustCompile(`^(?:[^@/:]+@)?([^/:]+):(.+)$`)

// RepoID is a repository's address as a relative path, host/owner/repo,
// the same however the address is written: git@host:o/r.git, ssh://, https://,
// and the git+ forms of flake references.
func RepoID(address string) (string, error) {
	a := strings.TrimPrefix(address, "git+")
	var host, path string
	if m := scpLike.FindStringSubmatch(a); m != nil && !strings.Contains(a, "://") {
		host, path = m[1], m[2]
	} else {
		u, err := url.Parse(a)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("%q is not a repository address", address)
		}
		host, path = u.Hostname(), u.Path
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%q is not a repository address", address)
		}
	}
	return strings.ToLower(host) + "/" + path, nil
}

// SignersCopy is where the copy of the signers for the repository at address
// lives.
func SignersCopy(address string) (string, error) {
	id, err := RepoID(address)
	if err != nil {
		return "", err
	}
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repos", filepath.FromSlash(id), "allowed_signers"), nil
}

// WriteSignersCopy writes the copy, its folder readable by the owner only.
func WriteSignersCopy(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SignerKey is one line of an allowed_signers file.
type SignerKey struct {
	Label       string // the principals, free text here
	Type        string // ssh-ed25519, sk-ssh-ed25519@openssh.com, ...
	Blob        string // the key, base64
	Fingerprint string // SHA256:…, as ssh-keygen -l prints it
}

func (k SignerKey) String() string {
	return k.Label + " " + k.Type + " " + k.Fingerprint
}

var keyType = regexp.MustCompile(`^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com)$`)

// ParseSigners reads an allowed_signers file: per line, principals, options
// such as namespaces="git", the key type and the key. Blank lines and
// comments are skipped.
func ParseSigners(data []byte) ([]SignerKey, error) {
	var keys []SignerKey
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitQuoted(line)
		i := 1
		for i < len(fields) && !keyType.MatchString(fields[i]) {
			i++
		}
		if i+1 >= len(fields) {
			return nil, fmt.Errorf("line %d: no key in %q", n, line)
		}
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil {
			return nil, fmt.Errorf("line %d: the key is not base64: %w", n, err)
		}
		sum := sha256.Sum256(blob)
		keys = append(keys, SignerKey{
			Label: fields[0], Type: fields[i], Blob: fields[i+1],
			Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]),
		})
	}
	return keys, sc.Err()
}

// splitQuoted splits on spaces outside double quotes.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// DiffSigners lists the keys in now that were not in before, and the other
// way round, by key: a changed label alone is no change of trust.
func DiffSigners(before, now []SignerKey) (added, removed []SignerKey) {
	in := func(keys []SignerKey, k SignerKey) bool {
		for _, o := range keys {
			if o.Type == k.Type && o.Blob == k.Blob {
				return true
			}
		}
		return false
	}
	for _, k := range now {
		if !in(before, k) {
			added = append(added, k)
		}
	}
	for _, k := range before {
		if !in(now, k) {
			removed = append(removed, k)
		}
	}
	return added, removed
}

// ErrNoSignersCopy is returned when there is no copy for a repository yet.
var ErrNoSignersCopy = errors.New("no copy of the keys you trust")

// TrustedSigners reads the copy for the repository at address.
func TrustedSigners(address string) (path string, keys []SignerKey, err error) {
	path, err = SignersCopy(address)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil, ErrNoSignersCopy
	}
	if err != nil {
		return path, nil, err
	}
	keys, err = ParseSigners(data)
	return path, keys, err
}
