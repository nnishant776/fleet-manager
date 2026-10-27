package flows

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// The port of fleet-secrets: the same five commands, arguments, branches,
// files and commit messages as lib/apps/fleet-secrets.nix, as guided steps.
// Each host has a branch, host/<name>; a command works in a temporary
// worktree of it.

// enrolledHosts are the hosts that have a branch, in the clone or fetched
// from origin.
func enrolledHosts(x *engine.Ctx) ([]string, error) { return secrets.EnrolledHosts(x) }

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

// SecretsHostEnroll admits a new host.
var SecretsHostEnroll = &engine.Flow{
	Path:    []string{"secrets", "host-enroll"},
	Title:   "Admit a host from its enrollment file",
	Short:   "Admit a new host from its enrollment file",
	Long:    "Creates the host's branch, host/<host>, with its empty encrypted file, encrypted to the host and\nyour recovery recipient, and the enrollment file as a record; commits, and shows the deploy key to\nregister. A host that has its branch with this identity already is left as it is. Push the branch after.",
	Example: "  fleet secrets host-enroll laptop1 --from laptop1.enroll\n  ssh support@laptop1 sudo cat /var/lib/fleet/laptop1.enroll | fleet secrets host-enroll laptop1 --from -",
	Args:    []string{"host"},
	Inputs:  secretsInputs(secrets.HostName("Which host?"), secrets.FromInput),
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(true),
		secrets.ReadEnrollment(),
		secrets.EnrollTarget(),
		secrets.Unless(secrets.Unlock()),
		secrets.Unless(secrets.OpenWork()),
		secrets.Unless(secrets.CreateEmpty("Create hosts/{{.host}}.yaml", "hosts/{{.host}}.yaml", "recipient")),
		secrets.Unless(secrets.WriteEnrollment()),
		secrets.Unless(secrets.Commit("host-enroll: {{.host}}", "enroll", "hosts/{{.host}}.yaml")),
		secrets.Unless(secrets.CloseWork()),
		secrets.Unless(deployKeyText("")),
	},
}

// SecretsHostRekey points a known host at a new identity.
var SecretsHostRekey = &engine.Flow{
	Path:    []string{"secrets", "host-rekey"},
	Title:   "Re-key a host in the secrets repository",
	Short:   "Point a known host at a new identity",
	Long:    "For a host reinstalled with a new identity: replaces its old recipient by the new one in every\nfile of its branch, host/<host>, replaces its enrollment record, commits, and shows the new deploy\nkey. Push the branch after.",
	Example: "  fleet secrets host-rekey laptop1 --from laptop1.enroll",
	Args:    []string{"host"},
	Inputs:  secretsInputs(knownHost(), secrets.FromInput),
	Needs:   []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(true),
		secrets.ReadEnrollment(),
		secrets.ExistingBranch("host '{{.host}}' is not enrolled; use host-enroll", true),
		{
			Title: "Check it is a new identity",
			Why:   "A host that holds this identity already has nothing to re-key.",
			Do: engine.Func(func(x *engine.Ctx) error {
				if x.Value("host_recipient") == x.Value("recipient") {
					return fmt.Errorf("host '%s' holds this identity already; nothing to re-key", x.Value("host"))
				}
				x.UI.OK("it differs from " + x.Value("host_recipient"))
				return nil
			}),
		},
		secrets.Unlock(),
		{
			Title: "Re-key the files of the users {{.host}} lists",
			Why: "A user that hosts/{{.host}}.yaml lists keeps their file for {{.host}} on their own branch, user/<user>, which the host decrypts too: the old recipient is replaced by the new one there as well, one commit per user. " +
				"A file that has the new recipient already is left alone, so a run that stopped can be repeated.",
			Do: engine.Func(rekeyListedUsers),
		},
		secrets.OpenWork(),
		secrets.Rotate("Re-encrypt {{.host}}'s files to its new identity",
			"Its own file and its users' files: the old recipient is replaced by the new one, the recovery recipient stays."),
		secrets.WriteEnrollment(),
		secrets.Commit("host-rekey: {{.host}}", "enroll", "{{.files}}"),
		secrets.CloseWork(),
		deployKeyText("\n\nThen delete the host's previous deploy key on the forge."),
	},
}

// SecretsHostRetire removes a host and its files.
var SecretsHostRetire = &engine.Flow{
	Path:    []string{"secrets", "host-retire"},
	Title:   "Retire a host from the secrets repository",
	Short:   "Remove a host and its files",
	Long:    "Deletes the host's branch, host/<host>, in your clone after showing what it held, and lists what\nis still to do: deleting it on the forge, and the host's deploy key. Makes no commit.",
	Example: "  fleet secrets host-retire laptop1",
	Args:    []string{"host"},
	Inputs:  []engine.Input{secrets.RepoInput, knownHost()},
	Needs:   []string{"git"},
	Steps: []engine.Step{
		secrets.Open(false),
		secrets.ExistingBranch("host '{{.host}}' is not enrolled", false),
		{
			Title: "Show what host/{{.host}} holds",
			Why:   "The files that go with the branch.",
			Do:    engine.Cmd("git", "ls-tree", "-r", "--name-only", "{{.ref}}").In("{{.repo}}").ReadOnly(),
		},
		{
			Title: "Delete the local branch host/{{.host}}",
			Why:   "With it go the host's files and its users' files on it.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("ref") != "refs/heads/host/"+x.Value("host") {
					return false, "there is no local branch; only origin's copy remains"
				}
				return true, ""
			},
			Do: engine.Cmd("git", "branch", "-D", "-q", "host/{{.host}}").In("{{.repo}}").OK("deleted"),
		},
		{
			Title: "Forget the commit you trusted on host/{{.host}}",
			Why:   "The branch is going away, so the record of its last trusted commit (a file in your fleet state folder) goes too; if the host is enrolled again, its branch is trusted afresh.",
			Do: engine.Func(func(x *engine.Ctx) error {
				if err := secrets.Retired(x, "host/"+x.Value("host")); err != nil {
					return err
				}
				x.UI.OK("removed")
				return nil
			}),
		},
		{
			Title: "Still to do",
			Why:   "The branch is gone from your clone only; the host's access ends when its deploy key goes.",
			Do: engine.Show("{{.host}} is retired. Still to do:\n" +
				"  1. Delete the branch on the forge, which takes it away from the host:\n" +
				"       git push origin --delete host/{{.host}}\n" +
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
	Long:    "Hashes the password (yescrypt) and stores the hash in users/<user>/<host>.yaml on the host's\nbranch, host/<host>, encrypted to that host and your recovery recipient. Hosts apply it at every\nactivation. For a user the host's file lists (fleet secrets user-enroll) it works on the user's branch,\nuser/<user>, instead, and a new file is encrypted to the user too. Push the branch after.",
	Example: "  fleet secrets user-password alice laptop1\n  printf %s \"$pw\" | fleet secrets user-password alice laptop1 --password-stdin --yes",
	Args:    []string{"user", "host"},
	Inputs: []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), knownHost(),
		{Name: "password", Prompt: "Password for {{.user}}", Help: "Hidden as you type", Secret: true, Twice: true}},
	Needs: []string{"git", "yq", "sops", "age", "mkpasswd"},
	Steps: []engine.Step{
		secrets.Open(true),
		secrets.ExistingBranch("host '{{.host}}' is not enrolled; use host-enroll first", true),
		secrets.Unlock(),
		secrets.ListedUser(),
		{
			Title: "Hash the password",
			Why:   "Only the yescrypt hash is stored; the password itself goes nowhere.",
			Do: engine.Cmd("mkpasswd", "-m", "yescrypt", "--stdin").ReadOnly().
				StdinFrom("password").StdinNote("the password").CaptureSecret("hash").OK("hashed"),
		},
		secrets.OpenWork(),
		createUserFile(),
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
		secrets.CloseWork(),
	},
}

// SecretsUserKey generates a user's SSH key on a host.
var SecretsUserKey = &engine.Flow{
	Path:    []string{"secrets", "user-key"},
	Title:   "Generate a user's SSH key on a host",
	Short:   "Generate the user's SSH key on that host",
	Long:    "Generates an ed25519 key in a memory-only directory, stores both halves in\nusers/<user>/<host>.yaml on the host's branch, host/<host>, shreds the plaintext, commits, and shows\nthe public key. For a user the host's file lists (fleet secrets user-enroll) it works on the user's branch,\nuser/<user>, instead. Push the branch after.",
	Example: "  fleet secrets user-key alice laptop1",
	Args:    []string{"user", "host"},
	Inputs:  []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), knownHost()},
	Needs:   []string{"git", "yq", "sops", "age", "ssh-keygen"},
	Steps: []engine.Step{
		secrets.Open(true),
		secrets.ExistingBranch("host '{{.host}}' is not enrolled; use host-enroll first", true),
		secrets.Unlock(),
		secrets.ListedUser(),
		{
			Title: "Check " + userFile() + " exists",
			Why:   "The key goes next to the user's password, which user-password creates.",
			Do: engine.Func(func(x *engine.Ctx) error {
				p, _ := x.Expand(userFile())
				if _, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "cat-file", "-e", x.Value("ref")+":"+p); err != nil {
					return fmt.Errorf("%s does not exist on %s; run user-password first", p, x.Value("branch"))
				}
				x.UI.OK(p + " exists")
				return nil
			}),
		},
		secrets.OpenWork(),
		{
			Title: "Generate the key and store it",
			Why:   "The private key exists in plain text only in a memory-only directory, overwritten and removed however this step ends.",
			Do:    engine.Func(generateUserKey),
		},
		secrets.Commit("user-key: {{.user}}@{{.host}}", userFile()),
		secrets.CloseWork(),
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

// createUserFile creates users/<user>/<host>.yaml in the worktree unless the
// branch has it, empty and encrypted to the host and the recovery recipient.
func createUserFile() engine.Step {
	return engine.Step{
		Title: "Create " + userFile(),
		Why:   "It does not exist yet; it is created empty and encrypted to {{.host}}, the user too when the user is listed, and your recovery recipient.",
		When: func(x *engine.Ctx) (bool, string) {
			p, _ := x.Expand(userFile())
			if _, err := os.Stat(filepath.Join(x.Value("work"), p)); err == nil {
				return false, "it exists already"
			}
			return true, ""
		},
		Do: secrets.CreateEmpty("", userFile(), "file_recipients").Do,
	}
}
