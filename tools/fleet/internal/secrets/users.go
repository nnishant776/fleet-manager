package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// A user listed in hosts/<host>.yaml (the reserved, encrypted key users, with
// users.<user> = { signing_key, age_recipient }) keeps their files on a branch
// of their own, user/<user>: users/<user>/<host>.yaml for each host, encrypted
// to the user, the host and the recovery recipient, and enroll, the user's
// enrollment file as given. A user not listed keeps them on the host's branch.

// Identity is a user's public identity: what users.<user> holds in a host's
// file, and what their enrollment file gives.
type Identity struct {
	AgeRecipient string `json:"age_recipient"`
	SigningKey   string `json:"signing_key"`
}

// UserBranchRef is the ref that holds the user's branch: the local branch, or
// else the one fetched from origin; empty when there is neither.
func UserBranchRef(x *engine.Ctx, user string) string {
	for _, ref := range []string{"refs/heads/user/" + user, "refs/remotes/origin/user/" + user} {
		if _, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", "--verify", "-q", ref); err == nil {
			return ref
		}
	}
	return ""
}

// VerifyTip checks the newest commit of a ref is signed by a key in
// gpg.ssh.allowedSignersFile, which a change builds on.
func VerifyTip(x *engine.Ctx, ref string) error {
	_, err := repoCmd("git", "verify-commit", ref).Explain(func(engine.Result) string {
		return "the newest commit of " + ref + " is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it):" +
			" look at it with git log --show-signature -1 " + ref + " before going on"
	}).RunQuiet(x)
	return err
}

// IsListed says whether hosts/<host>.yaml on hostRef lists the user. sops
// leaves the keys of a file in clear, so nothing is decrypted. The file is kept
// as the value host_file.
func IsListed(x *engine.Ctx, hostRef, host, user string) (bool, error) {
	doc, err := repoCmd("git", "show", hostRef+":hosts/"+host+".yaml").RunQuiet(x)
	if err != nil {
		return false, fmt.Errorf("%s has no hosts/%s.yaml", hostRef, host)
	}
	x.Values["host_file"] = doc.Stdout
	res, err := engine.Cmd("yq", `.users["`+user+`"] != null`).StdinFrom("host_file").StdinNote("the host's file").RunQuiet(x)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) == "true", nil
}

// ReadListing is IsListed, and for a user that is listed also the identity the
// file holds for them, decrypted from the file on stdin with the recovery
// identity unlocked.
func ReadListing(x *engine.Ctx, hostRef, host, user string) (bool, Identity, error) {
	var id Identity
	listed, err := IsListed(x, hostRef, host, user)
	if err != nil || !listed {
		return false, id, err
	}
	res, err := engine.Cmd("sops", "-d", "--input-type", "yaml", "--output-type", "json", "--extract",
		`["users"]["`+user+`"]`, "/dev/stdin").StdinFrom("host_file").StdinNote("the host's file").RunQuiet(x)
	if err != nil {
		return true, id, fmt.Errorf("could not read users.%s in hosts/%s.yaml: %w", user, host, err)
	}
	if err := json.Unmarshal([]byte(res.Stdout), &id); err != nil {
		return true, id, fmt.Errorf("users.%s in hosts/%s.yaml is not an identity: %w", user, host, err)
	}
	if !agePattern.MatchString(id.AgeRecipient) {
		return true, id, fmt.Errorf("users.%s in hosts/%s.yaml holds no age recipient", user, host)
	}
	return true, id, nil
}

// ListedUser finds where the user's file on the host is kept. A user that the
// host's file lists works on user/<user>: the value ref and branch become the
// user's, and a new file is encrypted to the host, the user and the recovery
// recipient, as the value file_recipients (without the recovery recipient,
// which CreateEmpty adds). Any other user works on the host's branch. It
// follows ExistingBranch and Unlock.
func ListedUser() engine.Step {
	return engine.Step{
		Title: "Look up where {{.user}}'s files on {{.host}} are kept",
		Why: "A user that hosts/{{.host}}.yaml lists keeps their files on a branch of their own, user/{{.user}}, and a new file is encrypted to the user as well; " +
			"any other user's files are on the host's branch. The list is encrypted, so this needs your recovery identity.",
		Do: engine.Func(func(x *engine.Ctx) error {
			host, user := x.Value("host"), x.Value("user")
			x.Values["file_recipients"] = x.Value("host_recipient")
			listed, id, err := ReadListing(x, x.Value("ref"), host, user)
			if err != nil {
				return err
			}
			if !listed {
				x.UI.OK(user + " is not listed in hosts/" + host + ".yaml: their files are on " + x.Value("branch"))
				return nil
			}
			ref := UserBranchRef(x, user)
			if ref == "" {
				return fmt.Errorf("%s is listed in hosts/%s.yaml, but there is no user/%s in %s, local or fetched from origin: fetch it", user, host, user, x.Value("repo"))
			}
			key, err := UserSigningKey(x, user)
			if err != nil {
				return err
			}
			if err := VerifyUserTip(x, ref, user, key); err != nil {
				return err
			}
			x.Values["ref"], x.Values["branch"] = ref, "user/"+user
			x.Values["file_recipients"] = x.Value("host_recipient") + "," + id.AgeRecipient
			x.UI.OK(user + " is listed: their files are on " + ref + ", its newest commit signed by a trusted key or by their own")
			return nil
		}),
	}
}

// WorktreeAt makes a temporary worktree of branch from ref, empty and
// orphaned when ref is empty and made from origin's when ref is not the
// local branch. A run that stops takes the worktree away, and a branch made
// for it, until Closed. It returns the directory.
func WorktreeAt(x *engine.Ctx, branch, ref string) (string, error) {
	dir := "<worktree>"
	if !x.Options.DryRun {
		var err error
		if dir, err = os.MkdirTemp("", "fleet-secrets-"); err != nil {
			return "", err
		}
	}
	var args []string
	madeBranch := false
	switch {
	case ref == "":
		args = []string{"worktree", "add", "-q", "--orphan", "-b", branch, dir}
	case ref == "refs/heads/"+branch:
		args = []string{"worktree", "add", "-q", dir, branch}
	default:
		args = []string{"worktree", "add", "-q", "-b", branch, dir, ref}
		madeBranch = true
	}
	if _, err := repoCmd("git", args...).Run(x); err != nil {
		if !x.Options.DryRun {
			os.Remove(dir)
		}
		return "", err
	}
	undo := []engine.Action{repoCmd("git", "worktree", "remove", "--force", dir).OK("the worktree of " + branch + " is removed")}
	if madeBranch {
		undo = append(undo, repoCmd("git", "branch", "-D", "-q", branch).OK("and the branch made for it"))
	}
	x.Undo("worktree "+branch, engine.Seq(undo...))
	return dir, nil
}

// CloseWorktree removes a worktree made by WorktreeAt once its commit is
// made; the branch it made stays.
func CloseWorktree(x *engine.Ctx, branch, dir string) error {
	if _, err := repoCmd("git", "worktree", "remove", "--force", dir).OK("removed the worktree of " + branch).Run(x); err != nil {
		return err
	}
	x.Done("worktree " + branch)
	return nil
}

// EnrolledHosts are the hosts that have a branch, in the clone or fetched from
// origin, sorted.
func EnrolledHosts(x *engine.Ctx) ([]string, error) {
	res, err := engine.Cmd("git", "for-each-ref", "--format=%(refname)", "refs/heads/host/", "refs/remotes/origin/host/").
		In("{{.repo}}").RunQuiet(x)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, ref := range strings.Fields(res.Stdout) {
		h, ok := strings.CutPrefix(ref, "refs/heads/host/")
		if !ok {
			h = strings.TrimPrefix(ref, "refs/remotes/origin/host/")
		}
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	return hosts, nil
}

// UserSigningKey is the signing key that the host files listing the user hold
// for them, or "" when no host lists the user. Every host that lists them has
// its tip verified (a pushed, unsigned commit must not choose who may sign a
// user's branch), and they must all hold the same key. It needs the recovery
// identity unlocked.
func UserSigningKey(x *engine.Ctx, user string) (string, error) {
	hosts, err := EnrolledHosts(x)
	if err != nil {
		return "", err
	}
	key, from := "", ""
	for _, h := range hosts {
		ref := BranchRef(x, h)
		listed, err := IsListed(x, ref, h, user)
		if err != nil {
			return "", err
		}
		if !listed {
			continue
		}
		if err := VerifyTip(x, ref); err != nil {
			return "", err
		}
		_, id, err := ReadListing(x, ref, h, user)
		if err != nil {
			return "", err
		}
		if key != "" && id.SigningKey != key {
			return "", fmt.Errorf("hosts/%s.yaml and hosts/%s.yaml list %s with different signing keys", from, h, user)
		}
		key, from = id.SigningKey, h
	}
	return key, nil
}

// allowedSigners is the file git checks signatures against, as an absolute
// path; empty when there is none set.
func allowedSigners(x *engine.Ctx) string {
	p, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "config", "--get", "gpg.ssh.allowedSignersFile")
	if err != nil || p == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, rest)
		}
	}
	if !filepath.IsAbs(p) {
		top, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", "--show-toplevel")
		if err != nil {
			return ""
		}
		p = filepath.Join(top, p)
	}
	return p
}

// VerifyUserTip checks the newest commit of a user's branch is signed by a key
// in the maintainer's gpg.ssh.allowedSignersFile or by the user's own signing
// key (key, when not empty). The user's key goes into a temporary copy of the
// signers, never into the maintainer's own.
func VerifyUserTip(x *engine.Ctx, ref, user, key string) error {
	if key == "" {
		return VerifyTip(x, ref)
	}
	var signers []byte
	if p := allowedSigners(x); p != "" {
		signers, _ = os.ReadFile(p)
	}
	return verifyAgainst(x, ref, user, key, signers, "the newest commit of "+ref+" is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it) or by "+user+
		"'s own signing key: look at it with git log --show-signature -1 "+ref+" before going on")
}

// VerifyOwnTip is the user's own check of their branch: the newest commit of
// ref must be signed by their own signing key (key) or by a key in signers,
// the content of an allowed_signers file (their copy of the configuration
// repository's, which has the maintainers'); either may be empty. Both go into
// a temporary file, never into the user's git configuration.
func VerifyOwnTip(x *engine.Ctx, ref, user, key string, signers []byte) error {
	return verifyAgainst(x, ref, user, key, signers, "the newest commit of "+ref+" is signed neither by your signing key nor by a key in your copy of the configuration repository's signers"+
		" (fleet trust signers makes it; --signers names another file): look at it with git log --show-signature -1 "+ref+
		", and do not go on until you know who made it")
}

// verifyAgainst runs git verify-commit with a temporary allowed-signers file:
// signers, then the user's key as a line of its own.
func verifyAgainst(x *engine.Ctx, ref, user, key string, signers []byte, explain string) error {
	tmp, err := os.CreateTemp("", "fleet-signers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	text := strings.TrimRight(string(signers), "\n")
	if key != "" {
		fields := strings.Fields(key)
		if len(fields) < 2 {
			return fmt.Errorf("the signing key of %s is not an SSH public key", user)
		}
		text += "\n" + user + ` namespaces="git" ` + fields[0] + " " + fields[1]
	}
	if _, err := tmp.WriteString(strings.TrimLeft(text, "\n") + "\n"); err != nil {
		return err
	}
	tmp.Close()
	_, err = repoCmd("git", "-c", "gpg.ssh.allowedSignersFile="+tmp.Name(), "verify-commit", ref).Explain(func(engine.Result) string {
		return explain
	}).RunQuiet(x)
	return err
}
