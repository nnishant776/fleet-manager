// Package secrets is the port of fleet-secrets (lib/apps/fleet-secrets.nix):
// the maintainer's side of the secrets repository. It runs the same yq, sops,
// git, mkpasswd and ssh-keygen commands as the shell tool, in the same order,
// and leaves the repository in the same state: the same branches, one
// host/<name> per host, the same files on them, encrypted to the same
// recipients, and the same commit messages. tests/fleet-secrets-parity runs
// both and compares.
//
// A command for a host works in a temporary worktree of its branch, so the
// clone's own checkout stays on main. There is no registry of recipients: a
// file's recipients are in its own sops metadata, and the maintainer's
// recovery recipient is the one fleet setup caches.
package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// NamePattern is what host and user names may be: they end up in file names
// and in branch names.
const NamePattern = `^[a-z0-9][a-z0-9-]*$`

// RepoInput is the secrets checkout.
var RepoInput = engine.Input{
	Name:   "repo",
	Prompt: "Secrets repository checkout",
	Help:   "Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory",
	Default: func(*engine.Ctx) (string, error) {
		if v := os.Getenv("FLEET_SECRETS_REPO"); v != "" {
			return v, nil
		}
		return ".", nil
	},
}

// RecoveryInput is the maintainer's recovery identity.
var RecoveryInput = engine.Input{
	Name:     "recovery",
	Prompt:   "Recovery identity",
	Help:     "Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY",
	Optional: true,
	Default: func(*engine.Ctx) (string, error) {
		if v := os.Getenv("FLEET_RECOVERY"); v != "" {
			return v, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil
		}
		if p := filepath.Join(home, ".fleet", "recovery.age"); exists(p) {
			return p, nil
		}
		return "", nil
	},
}

// HostName and UserName are positional inputs.
func HostName(prompt string) engine.Input {
	return engine.Input{Name: "host", Prompt: prompt, Pattern: NamePattern,
		Help: "The host's name: lowercase letters, digits and '-'"}
}

func UserName() engine.Input {
	return engine.Input{Name: "user", Prompt: "Which user?", Pattern: NamePattern,
		Help: "The user's name: lowercase letters, digits and '-'"}
}

// FromInput is where an enrollment file comes from.
var FromInput = engine.Input{
	Name:   "from",
	Prompt: "Enrollment file",
	Help:   "The host's enrollment file, as written by fleet-secrets-sync init, or - for standard input",
}

// in is a path in the worktree the command works in.
func in(x *engine.Ctx, rel string) string { return filepath.Join(x.Value("work"), rel) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// repoCmd runs in the maintainer's clone, which stays on main.
func repoCmd(name string, args ...string) *engine.Command {
	return engine.Cmd(name, args...).In("{{.repo}}")
}

// cmd runs in the worktree of the host's branch.
func cmd(name string, args ...string) *engine.Command {
	return engine.Cmd(name, args...).In("{{.work}}")
}

// RecoveryCache is the file fleet setup keeps the maintainer's recovery
// recipient in: $XDG_STATE_HOME/fleet/recovery.pub.
func RecoveryCache() (string, error) {
	dir, err := fleet.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "recovery.pub"), nil
}

var agePattern = regexp.MustCompile(`^age1[0-9a-z]+$`)

// ReadRecoveryRecipient reads the cached recovery recipient, one age1… line.
func ReadRecoveryRecipient() (string, error) {
	p, err := RecoveryCache()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("no recovery recipient in %s: run fleet setup", p)
	}
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimSpace(line)
	if !agePattern.MatchString(line) {
		return "", fmt.Errorf("%s does not hold an age recipient: run fleet setup", p)
	}
	return line, nil
}

// Open checks the repository is one and that it can be trusted: git checks
// signatures against your own copy of the keys, outside every checkout, and
// does not run the repository's own hook. With recovery, it also checks that
// the recovery recipient is cached, since new files are encrypted to it, and
// keeps it as the value recovery_pub.
func Open(recovery bool) engine.Step {
	why := "The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change."
	if recovery {
		why += " New files are encrypted to the host and to your recovery recipient, which fleet setup caches."
	}
	return engine.Step{
		Title: "Open the secrets repository",
		Why:   why,
		Do: engine.Seq(
			engine.Checks(
				engine.Probe{
					What:   "{{.repo}} is a git repository",
					Cmd:    repoCmd("git", "rev-parse", "--git-dir"),
					Expect: func(string) (string, error) { return "", nil },
				},
				engine.Probe{
					What: "git checks signatures against your own copy of the keys",
					Cmd:  repoCmd("sh", "-c", signersOutsideCheckout),
					Expect: func(out string) (string, error) {
						if f, ok := strings.CutPrefix(strings.TrimSpace(out), "inside "); ok {
							return "", fmt.Errorf("git checks signatures against %s, which is inside a checkout, so a pushed commit could add "+
								"the key that signs it: run fleet setup, which points it at your own copy of the keys", f)
						}
						return "", nil
					},
				},
				engine.Probe{
					What: "git runs a copy of the hook you have read",
					Cmd:  repoCmd("git", "config", "--local", "--default", "", "--get", "core.hooksPath"),
					Expect: func(out string) (string, error) {
						if strings.TrimSpace(out) == ".githooks" {
							return "", errors.New("git runs the repository's own hook (core.hooksPath is .githooks):" +
								" run fleet setup, which installs a copy you have read in its place")
						}
						return "", nil
					},
				},
			),
			engine.Func(func(x *engine.Ctx) error {
				if !recovery {
					return nil
				}
				r, err := ReadRecoveryRecipient()
				if err != nil {
					return err
				}
				x.Values["recovery_pub"] = r
				x.UI.OK("your recovery recipient is cached: " + r)
				return nil
			}),
		),
	}
}

// Unlock decrypts the recovery identity into this run's environment only.
func Unlock() engine.Step {
	return engine.Step{
		Title: "Unlock your recovery identity",
		Why:   "Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.",
		When: func(x *engine.Ctx) (bool, string) {
			if x.Value("recovery") == "" {
				return false, "no recovery identity given; sops reads SOPS_AGE_KEY_FILE or SOPS_AGE_KEY"
			}
			return true, ""
		},
		Do: engine.Cmd("age", "-d", "{{.recovery}}").ReadOnly().Prompts().CaptureEnv("SOPS_AGE_KEY", "sops").OK("unlocked for this run"),
	}
}

// BranchRef is the ref that holds the host's branch: the local branch, or
// else the one fetched from origin; empty when there is neither.
func BranchRef(x *engine.Ctx, host string) string {
	for _, ref := range []string{"refs/heads/host/" + host, "refs/remotes/origin/host/" + host} {
		if _, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", "--verify", "-q", ref); err == nil {
			return ref
		}
	}
	return ""
}

// HostRecipient is the host's own recipient on a ref: the one in the sops
// metadata of hosts/<host>.yaml that is not the recovery recipient.
func HostRecipient(x *engine.Ctx, ref, host, recovery string) (string, error) {
	doc, err := repoCmd("git", "show", ref+":hosts/"+host+".yaml").RunQuiet(x)
	if err != nil {
		return "", fmt.Errorf("%s has no hosts/%s.yaml", ref, host)
	}
	x.Values["host_file"] = doc.Stdout
	res, err := engine.Cmd("yq", ".sops.age[].recipient").StdinFrom("host_file").StdinNote("the host's file").RunQuiet(x)
	if err != nil {
		return "", err
	}
	var others []string
	for _, r := range strings.Fields(res.Stdout) {
		if r != recovery {
			others = append(others, r)
		}
	}
	if len(others) != 1 {
		return "", fmt.Errorf("hosts/%s.yaml on %s should be encrypted to the recovery recipient and exactly one other, the host's", host, ref)
	}
	return others[0], nil
}

// EnrollTarget checks the host has no branch yet, or has one for this very
// identity, in which case it sets the value done and the later steps skip.
func EnrollTarget() engine.Step {
	return engine.Step{
		Title: "Look up host/{{.host}}",
		Why:   "A host's branch is host/<name>; a host that has one is enrolled. A branch of another identity is re-keyed, not enrolled over.",
		Do: engine.Func(func(x *engine.Ctx) error {
			host := x.Value("host")
			ref := BranchRef(x, host)
			x.Values["ref"] = ref
			if ref == "" {
				x.UI.OK("host/" + host + " does not exist yet")
				return nil
			}
			old, err := HostRecipient(x, ref, host, x.Value("recovery_pub"))
			if err != nil {
				return err
			}
			if old != x.Value("recipient") {
				return fmt.Errorf("host '%s' is already enrolled with another identity; use host-rekey", host)
			}
			x.Values["done"] = "true"
			x.UI.OK("host/" + host + " holds this identity already; nothing to do")
			return nil
		}),
	}
}

// Pending is the When of the steps that follow EnrollTarget.
func Pending(x *engine.Ctx) (bool, string) {
	if x.Value("done") == "true" {
		return false, "it is enrolled already, with this identity"
	}
	return true, ""
}

// Unless makes a step run only when Pending says so, besides its own When.
func Unless(s engine.Step) engine.Step {
	own := s.When
	s.When = func(x *engine.Ctx) (bool, string) {
		if ok, why := Pending(x); !ok {
			return false, why
		}
		if own != nil {
			return own(x)
		}
		return true, ""
	}
	return s
}

// ExistingBranch checks the host has a branch, host/<name>, and says
// otherwise when it has not. With work, the command builds on it, so its tip
// must be signed by a trusted key, and the host's recipient is kept as the
// value host_recipient.
func ExistingBranch(otherwise string, work bool) engine.Step {
	return engine.Step{
		Title: "Look up host/{{.host}}",
		Why:   "A host's branch is host/<name>; a host that has none is not enrolled.",
		Do: engine.Func(func(x *engine.Ctx) error {
			host := x.Value("host")
			ref := BranchRef(x, host)
			if ref == "" {
				msg, _ := x.Expand(otherwise)
				return errors.New(msg)
			}
			x.Values["ref"] = ref
			x.UI.OK(ref)
			if !work {
				return nil
			}
			if _, err := repoCmd("git", "verify-commit", "{{.ref}}").Explain(func(engine.Result) string {
				return "the newest commit of {{.ref}} is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it):" +
					" look at it with git log --show-signature -1 {{.ref}} before going on"
			}).RunQuiet(x); err != nil {
				return err
			}
			x.UI.OK("its newest commit is signed by a trusted key")
			r, err := HostRecipient(x, ref, host, x.Value("recovery_pub"))
			if err != nil {
				return err
			}
			x.Values["host_recipient"] = r
			x.UI.OK("the host's recipient is " + r)
			return nil
		}),
	}
}

// OpenWork makes the temporary worktree of host/<name> the command works in,
// as the value work: the branch is made, empty, when it has no ref yet, and
// made from origin's when only that exists. A run that stops takes the
// worktree, and a branch made for it, away again.
func OpenWork() engine.Step {
	return engine.Step{
		Title: "Open a worktree of host/{{.host}}",
		Why: "The change is made in a temporary worktree of the host's branch, so that your checkout stays on main and the commit holds this change and nothing else. " +
			"It goes away at the end, or when the run stops.",
		Do: engine.Func(func(x *engine.Ctx) error {
			dir := "<worktree>"
			if !x.Options.DryRun {
				var err error
				if dir, err = os.MkdirTemp("", "fleet-secrets-"); err != nil {
					return err
				}
			}
			x.Values["work"] = dir
			branch := "host/" + x.Value("host")
			var args []string
			madeBranch := false
			switch ref := x.Value("ref"); {
			case ref == "":
				args = []string{"worktree", "add", "-q", "--orphan", "-b", branch, "{{.work}}"}
			case ref == "refs/heads/"+branch:
				args = []string{"worktree", "add", "-q", "{{.work}}", branch}
			default:
				args = []string{"worktree", "add", "-q", "-b", branch, "{{.work}}", ref}
				madeBranch = true
			}
			if _, err := repoCmd("git", args...).Run(x); err != nil {
				if !x.Options.DryRun {
					os.Remove(dir)
				}
				return err
			}
			undo := []engine.Action{repoCmd("git", "worktree", "remove", "--force", "{{.work}}").OK("the worktree is removed")}
			if madeBranch {
				undo = append(undo, repoCmd("git", "branch", "-D", "-q", branch).OK("and the branch made for it"))
			}
			x.Undo("worktree", engine.Seq(undo...))
			return nil
		}),
	}
}

// CloseWork removes the worktree once the commit is made.
func CloseWork() engine.Step {
	return engine.Step{
		Title: "Remove the worktree",
		Why:   "It was only for this change; the commit is on host/{{.host}} in your clone.",
		Do: repoCmd("git", "worktree", "remove", "--force", "{{.work}}").OK("removed").After(func(x *engine.Ctx) error {
			x.Done("worktree")
			return nil
		}),
	}
}

// ReadEnrollment reads and checks the host's enrollment file, and keeps its
// age recipient and deploy key as values.
func ReadEnrollment() engine.Step {
	return engine.Step{
		Title: "Read the enrollment file",
		Why:   "It holds the host's two public keys: the age recipient its secrets are encrypted to, and the deploy key it fetches them with.",
		Do: engine.Func(func(x *engine.Ctx) error {
			var doc []byte
			var err error
			if from := x.Value("from"); from == "-" {
				doc, err = io.ReadAll(x.Stdin)
			} else if !exists(from) {
				return fmt.Errorf("no such file: %s", from)
			} else {
				doc, err = os.ReadFile(from)
			}
			if err != nil {
				return err
			}
			x.Values["enrollment"] = string(doc)
			field := map[string]string{}
			// Files written before the keys were renamed say deploy_pubkey.
			for _, f := range [][2]string{
				{"version", ".version"}, {"host", ".host"}, {"age_recipient", ".age_recipient"},
				{"secrets_deploy_pubkey", ".secrets_deploy_pubkey // .deploy_pubkey"},
			} {
				res, err := engine.Cmd("yq", f[1]).StdinFrom("enrollment").StdinNote("the enrollment file").RunQuiet(x)
				if err != nil {
					return err
				}
				field[f[0]] = strings.TrimSpace(res.Stdout)
			}
			if field["version"] != "1" {
				return fmt.Errorf("unsupported enrollment file version '%s'", field["version"])
			}
			if field["host"] != x.Value("host") {
				return fmt.Errorf("enrollment file is for host '%s', not '%s'", field["host"], x.Value("host"))
			}
			r := field["age_recipient"]
			if !strings.HasPrefix(r, "age1") || len(r) == 4 || strings.IndexFunc(r[4:], func(c rune) bool {
				return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z')
			}) >= 0 {
				return errors.New("malformed age_recipient in the enrollment file")
			}
			if !strings.HasPrefix(field["secrets_deploy_pubkey"], "ssh-ed25519 ") {
				return errors.New("secrets_deploy_pubkey in the enrollment file is not an ssh-ed25519 key")
			}
			// Absent from files written before hosts had a configuration key.
			res, err := engine.Cmd("yq", `.config_deploy_pubkey // ""`).StdinFrom("enrollment").StdinNote("the enrollment file").RunQuiet(x)
			if err != nil {
				return err
			}
			configKey := strings.TrimSpace(res.Stdout)
			if configKey != "" && !strings.HasPrefix(configKey, "ssh-ed25519 ") {
				return errors.New("config_deploy_pubkey in the enrollment file is not an ssh-ed25519 key")
			}
			x.Values["recipient"], x.Values["deploy_key"], x.Values["config_key"] = r, field["secrets_deploy_pubkey"], configKey
			x.UI.OK("for " + field["host"] + ", recipient " + r)
			return nil
		}),
	}
}

// WriteEnrollment writes the enrollment file as given to enroll in the
// worktree: the host's public identity, a record only.
func WriteEnrollment() engine.Step {
	return engine.Step{
		Title: "Write the enrollment record",
		Why:   "enroll holds the host's public identity as the host wrote it. Hosts do not read it; it is a record of what the branch was made for.",
		Do: engine.Func(func(x *engine.Ctx) error {
			if x.Options.DryRun {
				x.UI.Skip("dry run: not written")
				return nil
			}
			doc := strings.TrimRight(x.Value("enrollment"), "\n") + "\n"
			if err := os.WriteFile(in(x, "enroll"), []byte(doc), 0o644); err != nil {
				return err
			}
			x.UI.OK("wrote enroll")
			return nil
		}),
	}
}

// HostFiles are the files of the host on the worktree's branch that exist, in
// the shell's glob order.
func HostFiles(x *engine.Ctx, host string) []string {
	var files []string
	if exists(in(x, "hosts/"+host+".yaml")) {
		files = append(files, "hosts/"+host+".yaml")
	}
	users, _ := filepath.Glob(in(x, "users/*/"+host+".yaml"))
	for _, u := range users {
		rel, _ := filepath.Rel(x.Value("work"), u)
		files = append(files, rel)
	}
	return files
}

// Rotate replaces the host's old recipient (the value host_recipient) by its
// new one (the value recipient) in every file of the host on its branch,
// keeping them as the value "files", one per line, for the commit.
func Rotate(title, why string) engine.Step {
	return engine.Step{
		Title: title,
		Why:   why,
		Do: engine.Func(func(x *engine.Ctx) error {
			fs := HostFiles(x, x.Value("host"))
			if x.Options.DryRun && len(fs) == 0 {
				fs = []string{"hosts/" + x.Value("host") + ".yaml"}
			}
			x.Values["files"] = strings.Join(fs, "\n")
			for _, f := range fs {
				if _, err := cmd("sops", "rotate", "-i", "--rm-age", "{{.host_recipient}}", "--add-age", "{{.recipient}}", f).Run(x); err != nil {
					return err
				}
			}
			return nil
		}),
	}
}

// CreateEmpty creates an empty file encrypted to the host, whose recipient is
// in the value recipientValue, and to the recovery recipient.
func CreateEmpty(title, rel, recipientValue string) engine.Step {
	return engine.Step{
		Title: title,
		Why:   "sops encrypts an empty document to the host and to your recovery recipient.",
		Do: engine.Func(func(x *engine.Ctx) error {
			rel, err := x.Expand(rel)
			if err != nil {
				return err
			}
			tmp, err := os.CreateTemp("", "fleet-empty-*.yaml")
			if err != nil {
				return err
			}
			defer os.Remove(tmp.Name())
			if _, err := tmp.WriteString("{}\n"); err != nil {
				return err
			}
			tmp.Close()
			x.Values["recipients"] = x.Value(recipientValue) + "," + x.Value("recovery_pub")
			res, err := cmd("sops", "--encrypt", "--age", "{{.recipients}}", "--input-type", "yaml", "--output-type", "yaml",
				tmp.Name()).Quiet().OK("encrypted").Run(x)
			if err != nil {
				return fmt.Errorf("could not create %s: %w", rel, err)
			}
			if x.Options.DryRun {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(in(x, rel)), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(in(x, rel), []byte(strings.TrimRight(res.Stdout, "\n")+"\n"), 0o644); err != nil {
				return err
			}
			x.UI.OK("wrote " + rel)
			return nil
		}),
	}
}

// Commit adds paths (templated; "{{.files}}" expands to one per line) in the
// worktree and commits them with message, on the host's branch.
func Commit(message string, paths ...string) engine.Step {
	return engine.Step{
		Title: "Commit",
		Why:   "Signed with your key, if this checkout signs; hosts accept only signed commits. Push the branch afterwards.",
		Do: engine.Func(func(x *engine.Ctx) error {
			args := []string{"add"}
			for _, p := range paths {
				e, err := x.Expand(p)
				if err != nil {
					return err
				}
				args = append(args, strings.Fields(e)...)
			}
			if _, err := cmd("git", args...).Run(x); err != nil {
				return err
			}
			msg, err := x.Expand(message)
			if err != nil {
				return err
			}
			_, err = cmd("git", "commit", "-q", "-m", msg).OK("committed: " + msg).After(func(x *engine.Ctx) error {
				x.Settled()
				x.Kept("push host/{{.host}}", "the commit “"+msg+"” on host/{{.host}} in {{.repo}}, not pushed yet (git -C {{.repo}} push origin host/{{.host}})")
				return nil
			}).Run(x)
			return err
		}),
	}
}

// PushBranch pushes the host's branch, and drops the note that it is not.
func PushBranch() *engine.Command {
	return repoCmd("git", "push", "origin", "host/{{.host}}").After(func(x *engine.Ctx) error {
		x.Kept("push host/{{.host}}", "")
		return nil
	})
}

// JSON encodes s as a JSON string for sops set, the way the shell tool did
// with yq: trailing newlines dropped, as $(cat) dropped them.
func JSON(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(strings.TrimRight(s, "\n"))
	return strings.TrimRight(b.String(), "\n")
}

// Set sets one key of an encrypted file in the worktree from a value, passed
// on stdin so it never appears in a process listing.
func Set(file, key, value, note string) *engine.Command {
	return cmd("sops", "set", "--value-stdin", file, `["`+key+`"]`).StdinFrom(value).StdinNote(note)
}

// signersOutsideCheckout prints "inside FILE" when git's
// gpg.ssh.allowedSignersFile lies inside a git checkout, where a pushed commit
// could add the key that signs it; fleet setup points it at your own copy
// (fleet trust signers). The shell fleet-secrets runs the same check.
const signersOutsideCheckout = `f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi`
