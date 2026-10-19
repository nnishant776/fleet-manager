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

// The port of fleet-secrets: the same five commands, arguments, files and
// commit messages as lib/apps/fleet-secrets.nix, as guided steps.

func enrolledHosts(x *engine.Ctx) ([]string, error) {
	res, err := engine.Cmd("yq", ".hosts | keys | .[]", "fleet.yaml").In("{{.repo}}").RunQuiet(x)
	if err != nil {
		return nil, err
	}
	return strings.Fields(res.Stdout), nil
}

func secretsInputs(host engine.Input, extra ...engine.Input) []engine.Input {
	return append([]engine.Input{secrets.RepoInput, secrets.RecoveryInput, host}, extra...)
}

func knownHost() engine.Input {
	h := secrets.HostName("Which host?")
	h.Choices = enrolledHosts
	return h
}

func deployKeyText(extra string) engine.Step {
	return engine.Step{
		Title: "Register {{.host}}'s deploy key",
		Why:   "The host fetches this repository with it; it must be read-only.",
		Do: engine.Seq(
			engine.Show("Secrets deploy key of {{.host}}:\n"+
				"  {{.deploy_key}}\n\n"+
				"Register it read-only on the secrets repository (the web page, Settings -> Deploy keys,\n"+
				"is the safer route), or with:\n"+
				"  gh repo deploy-key add <file holding the key above> --title {{.host}}"+extra),
			engine.Func(configKeyText),
		),
	}
}

// configKeyText shows the host's configuration key, which enrollment files
// list since hosts have one, for a fleet whose configuration is private.
func configKeyText(x *engine.Ctx) error {
	if x.Value("config_key") == "" {
		return nil
	}
	x.UI.Blank()
	x.Say("Configuration deploy key of {{.host}}:\n" +
		"  {{.config_key}}\n\n" +
		"If its configuration repository is private (an SSH conf.fleet.repo.url),\n" +
		"register this one read-only there, the same way:\n" +
		"  gh repo deploy-key add <file holding the key above> --repo <owner>/<configuration repository> --title {{.host}}")
	return nil
}

func absent(rel, msg string) engine.Step {
	return engine.Step{
		Title: "Check " + rel + " does not exist yet",
		Why:   "A host's file is created here, empty, by this command only.",
		Do: engine.Func(func(x *engine.Ctx) error {
			p, err := x.Expand(rel)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(x.Value("repo"), p)); err == nil {
				m, _ := x.Expand(msg)
				return errors.New(m)
			}
			x.UI.OK(p + " does not exist yet")
			return nil
		}),
	}
}

// SecretsHostEnroll admits a new host.
var SecretsHostEnroll = &engine.Flow{
	Path:    []string{"secrets", "host-enroll"},
	Title:   "Admit a host from its enrollment file",
	Short:   "Admit a new host from its enrollment file",
	Long:    "Records the host's age recipient in fleet.yaml, renders .sops.yaml, creates its\nempty encrypted file, commits, and shows the deploy key to register.",
	Example: "  fleet secrets host-enroll laptop1 --from laptop1.enroll\n  ssh support@laptop1 sudo cat /var/lib/fleet/laptop1.enroll | fleet secrets host-enroll laptop1 --from -",
	Args:    []string{"host"},
	Inputs:  secretsInputs(secrets.HostName("Which host?"), secrets.FromInput),
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(false, "host '{{.host}}' is already enrolled; use host-rekey"),
		absent("hosts/{{.host}}.yaml", "hosts/{{.host}}.yaml exists already"),
		secrets.ReadEnrollment(),
		secrets.Unlock(),
		secrets.SetRecipient(),
		secrets.Render(),
		// Files of this host that exist already, such as a user file written
		// before the host was enrolled, are now covered by its rules.
		secrets.UpdateKeys("Re-encrypt {{.host}}'s existing files",
			"Any file already under this host's rules gets its recipients.",
			func(x *engine.Ctx) []string { return secrets.HostFiles(x, x.Value("host")) }),
		secrets.CreateEmpty("Create hosts/{{.host}}.yaml", "hosts/{{.host}}.yaml"),
		secrets.Commit("host-enroll: {{.host}}", "fleet.yaml", ".sops.yaml", "hosts/{{.host}}.yaml", "{{.files}}"),
		deployKeyText(""),
	},
}

// SecretsHostRekey points a known host at a new identity.
var SecretsHostRekey = &engine.Flow{
	Path:    []string{"secrets", "host-rekey"},
	Title:   "Re-key a host in the secrets repository",
	Short:   "Point a known host at a new identity",
	Long:    "For a host reinstalled with a new identity: records its new recipient, renders\n.sops.yaml, re-encrypts its files to it, commits, and shows the new deploy key.",
	Example: "  fleet secrets host-rekey laptop1 --from laptop1.enroll",
	Args:    []string{"host"},
	Inputs:  secretsInputs(knownHost(), secrets.FromInput),
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(true, "host '{{.host}}' is not enrolled; use host-enroll"),
		secrets.ReadEnrollment(),
		secrets.Unlock(),
		secrets.SetRecipient(),
		secrets.Render(),
		secrets.UpdateKeys("Re-encrypt {{.host}}'s files to its new identity",
			"Its own file and its users' files.",
			func(x *engine.Ctx) []string { return secrets.HostFiles(x, x.Value("host")) }),
		secrets.Commit("host-rekey: {{.host}}", "fleet.yaml", ".sops.yaml", "{{.files}}"),
		deployKeyText("\n\nThen delete the host's previous deploy key on the forge."),
	},
}

// SecretsHostRetire removes a host and its files.
var SecretsHostRetire = &engine.Flow{
	Path:    []string{"secrets", "host-retire"},
	Title:   "Retire a host from the secrets repository",
	Short:   "Remove a host and its files",
	Long:    "Removes the host from fleet.yaml and .sops.yaml, deletes its files, re-encrypts\nthe rest as a safety net, commits, and lists what is still to do.",
	Example: "  fleet secrets host-retire laptop1",
	Args:    []string{"host"},
	Inputs:  secretsInputs(knownHost()),
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(true, "host '{{.host}}' is not enrolled"),
		secrets.Unlock(),
		{
			Title: "Remove {{.host}} from fleet.yaml",
			Why:   "Its recipient goes, and with it the two rules .sops.yaml has for it.",
			Do: engine.Cmd("yq", "-i", "del(.hosts[strenv(h)])", "fleet.yaml").In("{{.repo}}").Env("h={{.host}}").
				Before(secrets.Restoring("fleet.yaml")),
		},
		secrets.Render(),
		{
			Title: "Delete {{.host}}'s files",
			Why:   "Its own file and its users' files on it.",
			Do: engine.Func(func(x *engine.Ctx) error {
				files := secrets.HostFiles(x, x.Value("host"))
				if len(files) == 0 {
					x.UI.Skip("it has no files")
				}
				for _, f := range files {
					if _, err := engine.Cmd("git", "rm", "-q", "-f", f).In("{{.repo}}").Before(secrets.Restoring(f)).Run(x); err != nil {
						return err
					}
				}
				return nil
			}),
		},
		// Other files never included this host; re-encrypting them is a safety net.
		secrets.UpdateKeys("Re-encrypt every other file", "A safety net: no other file ever included this host.", secrets.AllFiles),
		secrets.Commit("host-retire: {{.host}}", "fleet.yaml", ".sops.yaml", "{{.files}}"),
		{
			Title: "Still to do",
			Why:   "The commit takes effect when pushed; the host's access ends when its deploy key goes.",
			Do: engine.Show("{{.host}} is retired. Still to do:\n" +
				"  1. Push this commit.\n" +
				"  2. Delete the host's deploy key on the forge; that takes effect at once.\n" +
				"  3. Rotate what the host could decrypt, which is only its own files:\n" +
				"     its users' passwords and SSH keys on {{.host}} (revoke their public keys\n" +
				"     on the forge now) and whatever was in hosts/{{.host}}.yaml."),
		},
	},
}

func userFile() string { return "users/{{.user}}/{{.host}}.yaml" }

// SecretsUserPassword sets a user's password on a host.
var SecretsUserPassword = &engine.Flow{
	Path:    []string{"secrets", "user-password"},
	Title:   "Set a user's password on a host",
	Short:   "Set the user's password on that host",
	Long:    "Hashes the password (yescrypt) and stores the hash in users/<user>/<host>.yaml,\nencrypted to that host and you. Hosts apply it at every activation.",
	Example: "  fleet secrets user-password alice laptop1\n  printf %s \"$pw\" | fleet secrets user-password alice laptop1 --password-stdin --yes",
	Args:    []string{"user", "host"},
	Inputs: []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), knownHost(),
		{Name: "password", Prompt: "Password for {{.user}}", Help: "Hidden as you type", Secret: true, Twice: true}},
	Needs: []string{"git", "yq", "sops", "age", "mkpasswd"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(true, "host '{{.host}}' is not enrolled; use host-enroll first"),
		secrets.Unlock(),
		{
			Title: "Hash the password",
			Why:   "Only the yescrypt hash is stored; the password itself goes nowhere.",
			Do: engine.Cmd("mkpasswd", "-m", "yescrypt", "--stdin").ReadOnly().
				StdinFrom("password").StdinNote("the password").CaptureSecret("hash").OK("hashed"),
		},
		{
			Title: "Create " + userFile(),
			Why:   "It does not exist yet; it is created empty and encrypted to {{.host}} and you.",
			When: func(x *engine.Ctx) (bool, string) {
				p, _ := x.Expand(userFile())
				if _, err := os.Stat(filepath.Join(x.Value("repo"), p)); err == nil {
					return false, "it exists already"
				}
				return true, ""
			},
			Do: secrets.CreateEmpty("", userFile()).Do,
		},
		{
			Title: "Store the hash in " + userFile(),
			Why:   "As the key password, which hosts read when accounts.<user>.secrets.password is on.",
			Do: engine.Func(func(x *engine.Ctx) error {
				x.Values["hash_json"] = secrets.JSON(x.Value("hash"))
				_, err := secrets.Set(userFile(), "password", "hash_json", "the password hash").Run(x)
				return err
			}),
		},
		secrets.Commit("user-password: {{.user}}@{{.host}}", userFile()),
	},
}

// SecretsUserKey generates a user's SSH key on a host.
var SecretsUserKey = &engine.Flow{
	Path:    []string{"secrets", "user-key"},
	Title:   "Generate a user's SSH key on a host",
	Short:   "Generate the user's SSH key on that host",
	Long:    "Generates an ed25519 key in a memory-only directory, stores both halves in\nusers/<user>/<host>.yaml, shreds the plaintext, commits, and shows the public key.",
	Example: "  fleet secrets user-key alice laptop1",
	Args:    []string{"user", "host"},
	Inputs:  []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), knownHost()},
	Needs:   []string{"git", "yq", "sops", "age", "ssh-keygen"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(true, "host '{{.host}}' is not enrolled; use host-enroll first"),
		{
			Title: "Check " + userFile() + " exists",
			Why:   "The key goes next to the user's password, which user-password creates.",
			Do: engine.Func(func(x *engine.Ctx) error {
				p, _ := x.Expand(userFile())
				if _, err := os.Stat(filepath.Join(x.Value("repo"), p)); err != nil {
					return fmt.Errorf("%s does not exist; run user-password first", p)
				}
				x.UI.OK(p + " exists")
				return nil
			}),
		},
		secrets.Unlock(),
		{
			Title: "Generate the key and store it",
			Why:   "The private key exists in plain text only in a memory-only directory, overwritten and removed however this step ends.",
			Do:    engine.Func(generateUserKey),
		},
		secrets.Commit("user-key: {{.user}}@{{.host}}", userFile()),
		{
			Title: "Register the public key",
			Why:   "Wherever {{.user}} needs it on {{.host}}'s behalf, such as their forge account.",
			Do:    engine.Show("Public key of {{.user}} on {{.host}}; register it on the forge:\n{{.public_key}}"),
		},
	},
}

func generateUserKey(x *engine.Ctx) error {
	base := fmt.Sprintf("/run/user/%d", os.Getuid())
	if st, err := os.Stat(base); err != nil || !st.IsDir() || writable(base) != nil {
		base = "/dev/shm"
	}
	dir, err := os.MkdirTemp(base, "fleet-key-")
	if err != nil {
		return err
	}
	defer shred(dir)
	x.Values["keydir"] = dir
	if _, err := engine.Cmd("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "{{.user}}@{{.host}}", "-f", "{{.keydir}}/key").
		ReadOnly().OK("generated in " + dir).Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		x.Values["public_key"] = "<public key>"
		return nil
	}
	priv, err := os.ReadFile(filepath.Join(dir, "key"))
	if err != nil {
		return err
	}
	pub, err := os.ReadFile(filepath.Join(dir, "key.pub"))
	if err != nil {
		return err
	}
	// The public key line ends in a newline of its own; it is not part of it.
	pubLine := strings.ReplaceAll(string(pub), "\n", "")
	x.Values["key_json"] = secrets.JSON(string(priv))
	x.Values["pub_json"] = secrets.JSON(pubLine)
	x.Values["public_key"] = pubLine
	if _, err := secrets.Set(userFile(), "ssh_key", "key_json", "the private key").Run(x); err != nil {
		return err
	}
	_, err = secrets.Set(userFile(), "ssh_key_pub", "pub_json", "the public key").Run(x)
	return err
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".fleet-probe-")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// shred overwrites the files in dir with zeros and removes it.
func shred(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(p); err == nil {
			_ = os.WriteFile(p, make([]byte, st.Size()), 0o600)
		}
		_ = os.Remove(p)
	}
	_ = os.Remove(dir)
}
