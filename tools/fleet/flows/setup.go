package flows

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// Setup is the maintainer's one-time setup, as the fleet-secrets README
// describes it, with every step that is done already skipped.
var Setup = &engine.Flow{
	Path:  []string{"setup"},
	Title: "Set up this machine as the maintainer's",
	Short: "Set up this machine as the maintainer's: recovery identity, signing key, both checkouts",
	Long: "The one-time setup: a passphrase-encrypted recovery identity, which can decrypt\n" +
		"every secret; an SSH key that signs commits, trusted by the fleet through\n" +
		"fleet/allowed_signers; both checkouts set to sign every commit; and the recovery\n" +
		"recipient, cached so that the secrets commands can encrypt to it without asking\n" +
		"for the passphrase. Each step that is done already is skipped, so it is safe to\n" +
		"run again.",
	Example: "  fleet setup --config ~/Projects/nixos-config --repo ~/Projects/fleet-secrets",
	Inputs: []engine.Input{
		{Name: "config", Prompt: "Configuration checkout", Help: "The fleet-manager checkout; defaults to this directory's flake, then /etc/nixos",
			Default: func(x *engine.Ctx) (string, error) {
				for _, d := range []string{".", "/etc/nixos"} {
					if fileExists(filepath.Join(d, "fleet", "allowed_signers")) || fileExists(filepath.Join(d, "flake.nix")) {
						return filepath.Abs(d)
					}
				}
				return "", nil
			}},
		secrets.RepoInput,
		{Name: "recovery", Prompt: "Recovery identity file", Help: "Where the passphrase-encrypted age identity lives; ~/.fleet/recovery.age by default",
			Default: func(*engine.Ctx) (string, error) {
				home, err := os.UserHomeDir()
				return filepath.Join(home, ".fleet", "recovery.age"), err
			}},
		{Name: "signing-key", Prompt: "Signing key", Help: "The SSH private key that signs commits; created if it does not exist",
			Default: func(*engine.Ctx) (string, error) {
				home, err := os.UserHomeDir()
				return filepath.Join(home, ".ssh", "fleet_manager_config_signing_key"), err
			}},
		{Name: "label", Prompt: "Label for your key in allowed_signers", Help: "Free text that git does not compare with anything; your GitHub noreply address, for example",
			Default: func(x *engine.Ctx) (string, error) {
				if v, err := engine.Silently(x, nil, "git", "-C", x.Value("config"), "config", "user.email"); err == nil && v != "" {
					return v, nil
				}
				return "maintainer", nil
			}},
	},
	Needs: []string{"git", "age", "age-keygen", "ssh-keygen"},
	Steps: []engine.Step{
		{
			Title: "Create your recovery identity",
			Why:   "It can decrypt every secret, so it is written straight into a passphrase-encrypted file and never touches the disk in clear. age asks for the passphrase twice. Keep a copy offline.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("recovery")) {
					return false, x.Value("recovery") + " exists"
				}
				return true, ""
			},
			Do: fleet.Creates(engine.Cmd("sh", "-c", `umask 077 && mkdir -p "$(dirname "$1")" && age-keygen | age -p -o "$1"`, "sh", "{{.recovery}}").
				Interactive().OK("written to {{.recovery}}"),
				"your recovery identity, {{.recovery}}; keep a copy offline", "{{.recovery}}"),
		},
		{
			Title: "Cache your recovery identity's recipient",
			Why: "The public half. Every secrets file is encrypted to it as well as to its host, and fleet secrets reads it from the cache, fleet/recovery.pub in your state folder (XDG_STATE_HOME), " +
				"so that it never needs the passphrase for that. age asks for the passphrase once, if the cache is missing.",
			When: recipientNotCached,
			Do:   engine.Func(cacheRecipient),
		},
		{
			Title: "Create your signing key",
			Why:   "An SSH key that signs every commit. Give it a passphrase, and load it into your agent with ssh-add when you work.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("signing-key")) {
					return false, x.Value("signing-key") + " exists"
				}
				return true, ""
			},
			Do: fleet.Creates(engine.Cmd("ssh-keygen", "-t", "ed25519", "-a", "100", "-C", "fleet signing", "-f", "{{.signing-key}}").Interactive(),
				"your signing key, {{.signing-key}}", "{{.signing-key}}", "{{.signing-key}}.pub"),
		},
		{
			Title: "Sign every commit in both checkouts",
			Why:   "Hosts accept only signed commits. These settings belong to the checkouts only, not your other repositories.",
			Do:    engine.Func(configureSigning),
		},
		{
			Title: "Install the secrets repository's commit check",
			Why: "Its pre-commit hook refuses anything under hosts/ or users/ that is not ciphertext. Git runs a hook as you, with your agent at hand, " +
				"so what is installed is a copy you have read, not the repository's own file, which anyone who can push could change.",
			When: hookNotInstalled,
			Do:   engine.Func(installHook),
		},
		{
			Title: "Trust your key in fleet/allowed_signers",
			Why:   "Hosts check every fetched commit against this file. Adding a key is itself a commit, which must be signed by a key already trusted, if there is one.",
			When:  keyNotTrusted,
			Do:    engine.Func(trustKey),
		},
		{
			Title: "Keep your copy of the configuration repository's keys",
			Why: "fleet and git check commits against your own copy of the keys, kept outside every checkout, not against fleet/allowed_signers, which a pushed commit could change. " +
				"The first copy is the one you approve now; fleet trust signers takes later changes.",
			When: func(x *engine.Ctx) (bool, string) {
				if !fileExists(filepath.Join(x.Value("config"), "fleet", "allowed_signers")) {
					return false, "the checkout has no fleet/allowed_signers yet"
				}
				return true, ""
			},
			Do: engine.Func(func(x *engine.Ctx) error {
				x.Values["flake"] = x.Value("config")
				if err := readCheckoutSigners(x); err != nil {
					return err
				}
				return keepSignersCopy(x)
			}),
		},
		{
			Title: "Next",
			Why:   "With that, hosts can be enrolled.",
			Do: engine.Show("Push the allowed_signers commit to fleet-manager's main if one was made, merging\n" +
				"locally: GitHub's merge buttons drop the signature. Then, for each host:\n" +
				"  fleet host enroll <host>"),
		},
	},
}

// recipientNotCached skips the step when the cache holds a recipient already.
func recipientNotCached(x *engine.Ctx) (bool, string) {
	path, err := secrets.RecoveryCache()
	if err != nil {
		return true, ""
	}
	x.Values["recipient_cache"] = path
	if r, err := secrets.ReadRecoveryRecipient(); err == nil {
		return false, path + " holds " + r
	}
	return true, ""
}

// cacheRecipient derives the recovery identity's recipient, which asks for
// its passphrase, and keeps it in the cache.
func cacheRecipient(x *engine.Ctx) error {
	path, err := secrets.RecoveryCache()
	if err != nil {
		return err
	}
	x.Values["recipient_cache"] = path
	if x.Options.DryRun && !fileExists(x.Value("recovery")) {
		x.UI.Skip("dry run: it would be read from the new identity")
		return nil
	}
	res, err := engine.Cmd("sh", "-c", `age -d "$1" | age-keygen -y`, "sh", "{{.recovery}}").
		ReadOnly().Prompts().Capture("recipient").OK("{{.recipient}}").Run(x)
	if err != nil {
		return err
	}
	if x.Options.DryRun {
		x.UI.Skip("dry run: not written to " + path)
		return nil
	}
	recipient := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(recipient, "age1") {
		return errors.New("that is not an age recipient: " + res.Stdout)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(recipient+"\n"), 0o644); err != nil {
		return err
	}
	x.UI.OK("wrote " + path)
	return nil
}

func configureSigning(x *engine.Ctx) error {
	// Signatures are checked against your own copy of the configuration
	// repository's keys, not the checkout's file, which a pushed commit could
	// change; the secrets repository is checked with the same keys, as on
	// hosts. A step below makes the copy.
	origin, err := engine.Silently(x, nil, "git", "-C", x.Value("config"), "remote", "get-url", "origin")
	if (err != nil || origin == "") && !x.Options.DryRun {
		return fmt.Errorf("%s has no origin: your copy of its keys is kept per repository, named after its address", x.Value("config"))
	}
	copyPath := "<your copy of the configuration repository's keys>"
	if origin != "" {
		if copyPath, err = fleet.SignersCopy(origin); err != nil {
			return err
		}
	}
	x.Values["signers_copy"] = copyPath
	settings := [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingKey", x.Value("signing-key") + ".pub"},
		{"commit.gpgSign", "true"},
		{"tag.gpgSign", "true"},
		{"gpg.ssh.allowedSignersFile", copyPath},
	}
	for _, repo := range []string{x.Value("config"), x.Value("repo")} {
		for _, kv := range settings {
			if err := setGitConfig(x, repo, kv[0], kv[1]); err != nil {
				return err
			}
		}
		x.UI.OK(repo + " signs every commit")
	}
	return nil
}

// setGitConfig sets a checkout's own git setting unless it already has that
// value.
func setGitConfig(x *engine.Ctx, repo, key, value string) error {
	current, _ := engine.Silently(x, nil, "git", "-C", repo, "config", "--local", "--get", key)
	if sameSetting(repo, current, value) {
		return nil
	}
	_, err := engine.Cmd("git", "-C", repo, "config", key, value).Run(x)
	return err
}

// hookPaths are the repository's own hook and the installed copy, always in
// the git directory's hooks, whatever core.hooksPath says: an older setup
// pointed it at .githooks, which would make the two the same file.
func hookPaths(x *engine.Ctx) (tracked, installed string) {
	repo := x.Value("repo")
	gitDir, err := engine.Silently(x, nil, "git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || gitDir == "" {
		gitDir = filepath.Join(repo, ".git")
	}
	return filepath.Join(repo, ".githooks", "pre-commit"), filepath.Join(gitDir, "hooks", "pre-commit")
}

func hookNotInstalled(x *engine.Ctx) (bool, string) {
	want, err := os.ReadFile(filepath.Join(x.Value("repo"), ".githooks", "pre-commit"))
	if err != nil {
		return false, "the repository has no .githooks/pre-commit"
	}
	_, installed := hookPaths(x)
	hooksPath, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "config", "--local", "--get", "core.hooksPath")
	if have, err := os.ReadFile(installed); err == nil && bytes.Equal(have, want) && hooksPath != ".githooks" {
		return false, "installed already, the same as the repository's"
	}
	return true, ""
}

// installHook shows the repository's hook, or how it changed since the copy
// was installed, and installs it once approved. Git stops running the
// repository's own file, which an older setup pointed core.hooksPath at.
func installHook(x *engine.Ctx) error {
	tracked, installed := hookPaths(x)
	want, err := os.ReadFile(tracked)
	if err != nil {
		return err
	}
	if _, err := os.Stat(installed); err == nil {
		diff, _ := engine.Silently(x, nil, "git", "diff", "--no-index", "--no-color", installed, tracked)
		x.UI.Note("The repository's hook differs from the copy installed in " + installed + ":")
		x.UI.Text(diff)
	} else {
		x.UI.Note("The repository's hook, " + tracked + ":")
		x.UI.Text(strings.TrimRight(string(want), "\n"))
	}
	ok, err := x.Approve("Install it as " + installed + "?")
	if !ok || err != nil {
		return err
	}
	switch hooksPath, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "config", "--local", "--get", "core.hooksPath"); hooksPath {
	case "":
	case ".githooks":
		if _, err := engine.Cmd("git", "-C", x.Value("repo"), "config", "--unset", "core.hooksPath").Run(x); err != nil {
			return err
		}
	default:
		x.UI.Note("core.hooksPath is " + hooksPath + ", so git runs the hooks there, not this copy.")
	}
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(installed, want, 0o755); err != nil {
		return err
	}
	x.UI.OK("installed " + installed)
	return nil
}

// sameSetting is whether a configured value is the wanted one, counting a
// path relative to the checkout (git runs from its top) or to ~ as the same.
func sameSetting(repo, current, want string) bool {
	if current == want || filepath.Join(repo, current) == want {
		return true
	}
	if rest, ok := strings.CutPrefix(current, "~/"); ok {
		home, err := os.UserHomeDir()
		return err == nil && filepath.Join(home, rest) == want
	}
	return false
}

func keyNotTrusted(x *engine.Ctx) (bool, string) {
	pub, err := os.ReadFile(x.Value("signing-key") + ".pub")
	if err != nil {
		return true, ""
	}
	key := strings.Join(strings.Fields(string(pub))[:2], " ")
	x.Values["signing_pub"] = key
	signers, _ := os.ReadFile(filepath.Join(x.Value("config"), "fleet", "allowed_signers"))
	if strings.Contains(string(signers), key) {
		return false, "fleet/allowed_signers trusts it already"
	}
	return true, ""
}

func trustKey(x *engine.Ctx) error {
	if x.Value("signing_pub") == "" {
		pub, err := os.ReadFile(x.Value("signing-key") + ".pub")
		if err != nil && !x.Options.DryRun {
			return err
		}
		if f := strings.Fields(string(pub)); len(f) >= 2 {
			x.Values["signing_pub"] = f[0] + " " + f[1]
		}
	}
	line := fmt.Sprintf("%s namespaces=\"git\" %s\n", x.Value("label"), x.Value("signing_pub"))
	x.UI.Text(strings.TrimRight(line, "\n"))
	path := filepath.Join(x.Value("config"), "fleet", "allowed_signers")
	ok, err := x.Approve("Add it to " + path + "?")
	if !ok || err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := secrets.RestoreOnStop(x, "{{.config}}", "fleet/allowed_signers"); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Your own key is one you trust: it goes into your copy as well, so the
	// commit it signs, which adds it, is not refused there.
	if mine, err := os.ReadFile(x.Value("signers_copy")); err == nil {
		if err := fleet.WriteSignersCopy(x.Value("signers_copy"), append(mine, line...)); err != nil {
			return err
		}
	}
	if _, err := engine.Cmd("git", "-C", "{{.config}}", "add", "fleet/allowed_signers").Before(func(x *engine.Ctx) error {
		return secrets.Unstage(x, "{{.config}}", []string{"fleet/allowed_signers"})
	}).Run(x); err != nil {
		return err
	}
	// Only this file, whatever else is staged.
	_, err = engine.Cmd("git", "-C", "{{.config}}", "commit", "-q", "-m", "feat(fleet): trust {{.label}}'s signing key", "--", "fleet/allowed_signers").
		OK("committed; push it to main, merging locally").After(func(x *engine.Ctx) error {
		x.Settled()
		x.Kept("push {{.config}}", "the commit that trusts your key in {{.config}}, not pushed yet: push it to main, merging locally")
		return nil
	}).Run(x)
	return err
}
