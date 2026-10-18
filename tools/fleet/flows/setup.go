package flows

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
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
		"fleet/allowed_signers; both checkouts set to sign every commit; and fleet.yaml's\n" +
		"maintainer recipient. Each step that is done already is skipped, so it is safe\n" +
		"to run again.",
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
	Needs: []string{"git", "yq", "age", "age-keygen", "ssh-keygen"},
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
			Do: engine.Cmd("sh", "-c", `umask 077 && mkdir -p "$(dirname "$1")" && age-keygen | age -p -o "$1"`, "sh", "{{.recovery}}").
				Interactive().OK("written to {{.recovery}}"),
		},
		{
			Title: "Find your recovery identity's recipient",
			Why:   "The public half, which fleet.yaml names as the maintainer. Read from fleet.yaml when it is set already; otherwise age asks for the passphrase.",
			Do:    engine.Func(recoveryRecipient),
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
			Do: engine.Cmd("ssh-keygen", "-t", "ed25519", "-a", "100", "-C", "fleet signing", "-f", "{{.signing-key}}").Interactive(),
		},
		{
			Title: "Sign every commit in both checkouts",
			Why:   "Hosts accept only signed commits. These settings belong to the checkouts only, not your other repositories.",
			Do:    engine.Func(configureSigning),
		},
		{
			Title: "Trust your key in fleet/allowed_signers",
			Why:   "Hosts check every fetched commit against this file. Adding a key is itself a commit, which must be signed by a key already trusted, if there is one.",
			When:  keyNotTrusted,
			Do:    engine.Func(trustKey),
		},
		{
			Title: "Name yourself as the maintainer in fleet.yaml",
			Why:   "Every secrets file is encrypted to the maintainer as well as to its host; fleet secrets refuses to enroll hosts until this is set.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("maintainer_set") == "true" {
					return false, "fleet.yaml names you already"
				}
				return true, ""
			},
			Do: engine.Seq(
				engine.Cmd("yq", "-i", ".maintainer = strenv(r)", "fleet.yaml").In("{{.repo}}").Env("r={{.recipient}}"),
				engine.Cmd("git", "commit", "-q", "-m", "fleet: set maintainer recipient", "fleet.yaml").In("{{.repo}}"),
				engine.Cmd("git", "push").In("{{.repo}}"),
			),
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

func recoveryRecipient(x *engine.Ctx) error {
	maintainer, err := engine.Cmd("yq", ".maintainer", "fleet.yaml").In("{{.repo}}").RunQuiet(x)
	if err != nil {
		return err
	}
	if m := strings.TrimSpace(maintainer.Stdout); strings.HasPrefix(m, "age1") {
		x.Values["recipient"], x.Values["maintainer_set"] = m, "true"
		x.UI.OK("fleet.yaml names " + m)
		return nil
	}
	if x.Options.DryRun && !fileExists(x.Value("recovery")) {
		x.UI.Skip("dry run: it would be read from the new identity")
		x.Values["recipient"] = "<your recipient>"
		return nil
	}
	res, err := engine.Cmd("sh", "-c", `age -d "$1" | age-keygen -y`, "sh", "{{.recovery}}").
		ReadOnly().Prompts().Capture("recipient").OK("{{.recipient}}").Run(x)
	if err != nil {
		return err
	}
	if !x.Options.DryRun && !strings.HasPrefix(strings.TrimSpace(res.Stdout), "age1") {
		return errors.New("that is not an age recipient: " + res.Stdout)
	}
	return nil
}

func configureSigning(x *engine.Ctx) error {
	settings := [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingKey", x.Value("signing-key") + ".pub"},
		{"commit.gpgSign", "true"},
		{"tag.gpgSign", "true"},
		{"gpg.ssh.allowedSignersFile", filepath.Join(x.Value("config"), "fleet", "allowed_signers")},
	}
	for _, repo := range []string{x.Value("config"), x.Value("repo")} {
		all := settings
		if repo == x.Value("repo") {
			// The secrets checkout also runs its pre-commit hook, which refuses
			// anything under hosts/ or users/ that is not ciphertext.
			all = append(all, [2]string{"core.hooksPath", ".githooks"})
		}
		for _, kv := range all {
			current, _ := engine.Silently(x, nil, "git", "-C", repo, "config", "--local", "--get", kv[0])
			if sameSetting(repo, current, kv[1]) {
				continue
			}
			if _, err := engine.Cmd("git", "-C", repo, "config", kv[0], kv[1]).Run(x); err != nil {
				return err
			}
		}
		x.UI.OK(repo + " signs every commit")
	}
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
	if _, err := engine.Cmd("git", "-C", "{{.config}}", "add", "fleet/allowed_signers").Run(x); err != nil {
		return err
	}
	// Only this file, whatever else is staged.
	_, err = engine.Cmd("git", "-C", "{{.config}}", "commit", "-q", "-m", "feat(fleet): trust {{.label}}'s signing key", "--", "fleet/allowed_signers").
		OK("committed; push it to main, merging locally").Run(x)
	return err
}
