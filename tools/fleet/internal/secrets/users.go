package secrets

import (
	"encoding/json"
	"fmt"
	"os"
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
			if err := VerifyTip(x, ref); err != nil {
				return err
			}
			x.Values["ref"], x.Values["branch"] = ref, "user/"+user
			x.Values["file_recipients"] = x.Value("host_recipient") + "," + id.AgeRecipient
			x.UI.OK(user + " is listed: their files are on " + ref + ", its newest commit signed by a trusted key")
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
