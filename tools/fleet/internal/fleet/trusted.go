package fleet

// Phase 2 of fleet trust: the trusted commit of each branch of each
// repository, kept beside the copy of its signers. A signed commit is not
// enough: an old one, served again, is signed too, and a tool that builds on
// it (publish cuts every host's branch from main as a new commit that you
// sign) would launder the rollback. Hosts refuse this with their accepted
// records; this gives the maintainer's machine the same.
//
// The record of a branch is one line, a full commit hash, in
// <state>/repos/<host>/<owner>/<repo>/trusted/<branch>; a branch with a / is
// a nested path (trusted/host/laptop1). A tip is accepted when there is no
// record yet (it becomes the record) or the record is that commit or an
// ancestor of it (the record moves to it). The shell fleet-secrets keeps the
// same files.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// TrustedPath is where the trusted commit of a branch of the repository at
// address is recorded.
func TrustedPath(address, branch string) (string, error) {
	id, err := RepoID(address)
	if err != nil {
		return "", err
	}
	for _, p := range strings.Split(branch, "/") {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%q is not a branch name", branch)
		}
	}
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repos", filepath.FromSlash(id), "trusted", filepath.FromSlash(branch)), nil
}

var commitHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ReadTrusted is the recorded commit of the branch, or "" when there is none.
func ReadTrusted(address, branch string) (string, error) {
	p, err := TrustedPath(address, branch)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	rec := strings.TrimSpace(string(data))
	if !commitHash.MatchString(rec) {
		return "", fmt.Errorf("%s does not hold a commit hash: remove it to trust the branch afresh", p)
	}
	return rec, nil
}

// WriteTrusted records commit as the branch's trusted one: atomically, the
// file readable by the owner only, its folders too.
func WriteTrusted(address, branch, commit string) error {
	p, err := TrustedPath(address, branch)
	if err != nil {
		return err
	}
	if !commitHash.MatchString(commit) {
		return fmt.Errorf("%q is not a full commit hash", commit)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(commit+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// RemoveTrusted drops the record of a branch, as the branch goes away. A
// branch without a record is fine.
func RemoveTrusted(address, branch string) error {
	p, err := TrustedPath(address, branch)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// OriginOf is the address of the checkout's origin.
func OriginOf(x *engine.Ctx, dir string) (string, error) {
	origin, err := engine.Silently(x, nil, "git", "-C", dir, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return "", fmt.Errorf("%s has no origin: trusted commits are kept per repository, named after its address", dir)
	}
	return origin, nil
}

func gitIn(x *engine.Ctx, dir string, args ...string) (engine.Result, error) {
	return x.Runner.Run(x.Context, engine.Spec{Name: "git", Args: append([]string{"-C", dir}, args...)})
}

// commitOf is the full hash of a ref or commit in the checkout.
func commitOf(x *engine.Ctx, dir, ref string) (string, error) {
	res, err := gitIn(x, dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil || strings.TrimSpace(res.Stdout) == "" {
		return "", fmt.Errorf("%s is no commit in %s", ref, dir)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// follows says whether commit a is b or an ancestor of it. A shallow clone
// may not have the history that tells, and then it says so rather than guess.
func follows(x *engine.Ctx, dir, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	shallow := false
	if res, err := gitIn(x, dir, "rev-parse", "--is-shallow-repository"); err == nil && strings.TrimSpace(res.Stdout) == "true" {
		shallow = true
	}
	cannotTell := fmt.Errorf("%s is a shallow clone, and cannot tell whether %s follows %s from the history it has: "+
		"fetch the rest (git -C %s fetch --unshallow) and try again", dir, b, a, dir)
	if _, err := gitIn(x, dir, "cat-file", "-e", a+"^{commit}"); err != nil {
		// With the whole history, a commit that b descends from is there.
		if shallow {
			return false, cannotTell
		}
		return false, nil
	}
	res, err := gitIn(x, dir, "merge-base", "--is-ancestor", a, b)
	switch {
	case err == nil:
		return true, nil
	case res.Code == 1:
		if shallow {
			return false, cannotTell
		}
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s in %s: %w", a, b, dir, err)
}

// GitIn runs git in the checkout dir and returns what it printed; a nonzero
// exit is an error with the result's Code set.
func GitIn(x *engine.Ctx, dir string, args ...string) (engine.Result, error) {
	return gitIn(x, dir, args...)
}

// CommitOf is the full hash of a ref or commit in the checkout.
func CommitOf(x *engine.Ctx, dir, ref string) (string, error) { return commitOf(x, dir, ref) }

// Follows says whether commit a is b or an ancestor of it; see follows.
func Follows(x *engine.Ctx, dir, a, b string) (bool, error) { return follows(x, dir, a, b) }

// Short is the first 12 characters of a commit hash.
func Short(c string) string { return short(c) }

func short(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// CheckTrusted accepts the tip (a ref or commit in the checkout dir) of the
// branch of the repository at address when it follows the branch's trusted
// commit, or when there is none yet, and moves the record to it. It comes
// after the signature check. Otherwise it refuses, and says what to run.
func CheckTrusted(x *engine.Ctx, dir, address, branch, tip string) error {
	return checkTrusted(x, dir, address, branch, tip, false)
}

// CheckTrustedLine is CheckTrusted for a commit that is pinned rather than
// followed, such as the fleet-manager commit a flake.lock holds: it may also
// be an ancestor of the trusted commit, then the record stays where it is.
func CheckTrustedLine(x *engine.Ctx, dir, address, branch, commit string) error {
	return checkTrusted(x, dir, address, branch, commit, true)
}

func checkTrusted(x *engine.Ctx, dir, address, branch, ref string, line bool) error {
	id, err := RepoID(address)
	if err != nil {
		return fmt.Errorf("%w: trusted commits are kept per repository, named after its address (a repository on this machine needs a file:// address, git remote set-url origin file://<path>)", err)
	}
	tip, err := commitOf(x, dir, ref)
	if err != nil {
		return err
	}
	rec, err := ReadTrusted(address, branch)
	if err != nil {
		return err
	}
	save := func() error {
		if x.Options.DryRun {
			return nil
		}
		return WriteTrusted(address, branch, tip)
	}
	what := id + " " + branch
	if rec == "" {
		x.UI.Note("You have no trusted commit for " + what + " yet: taking " + tip + " as the first. " +
			"Later tips are accepted only when they follow it.")
		return save()
	}
	if rec == tip {
		x.UI.OK(branch + " is at " + short(tip) + ", the commit you trusted")
		return nil
	}
	ok, err := follows(x, dir, rec, tip)
	if err != nil {
		return err
	}
	if ok {
		x.UI.OK(branch + " at " + short(tip) + " follows " + short(rec) + ", the commit you trusted")
		return save()
	}
	behind, err := follows(x, dir, tip, rec)
	if err != nil && line {
		return err
	}
	if behind && line {
		x.UI.OK(short(tip) + " is on the line of " + what + ", behind " + short(rec) + ", the commit you trusted")
		return nil
	}
	p, _ := TrustedPath(address, branch)
	why := fmt.Sprintf("%s at %s does not follow %s, the commit you trusted last: it could be an old commit served again. ", branch, tip, rec)
	if behind {
		// Most often a checkout of yours that is not up to date, since the
		// record moved on elsewhere (fleet repo sync, or another checkout).
		why = fmt.Sprintf("%s at %s does not follow %s, the commit you trusted last, but comes before it: "+
			"a checkout that is not up to date, or an old commit served again. If %s is your checkout, bring it up to date "+
			"(git pull) and run this again. ", branch, tip, rec, dir)
	}
	if name := NameOf(id); name != "" {
		return fmt.Errorf("%sIf the history was rewritten on purpose and you know why, review and accept it with fleet repo sync %s --accept-rewrite %s", why, name, branch)
	}
	return fmt.Errorf("%sIf the history was rewritten on purpose and you know why, trust it afresh: rm %s, then run this again; "+
		"or give the repository a name with fleet repo add and use fleet repo sync", why, p)
}

// AdvanceTrusted moves the record of a branch to what the checkout's local
// branch holds, after the tool pushed it: what a tool pushed is what it
// trusts. Nothing happens in a dry run.
func AdvanceTrusted(x *engine.Ctx, dir, branch string) error {
	if x.Options.DryRun {
		return nil
	}
	address, err := OriginOf(x, dir)
	if err != nil {
		return err
	}
	tip, err := commitOf(x, dir, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	return WriteTrusted(address, branch, tip)
}

// ForgetTrusted drops the record of a branch the tool deleted.
func ForgetTrusted(x *engine.Ctx, dir, branch string) error {
	if x.Options.DryRun {
		return nil
	}
	address, err := OriginOf(x, dir)
	if err != nil {
		return err
	}
	return RemoveTrusted(address, branch)
}
