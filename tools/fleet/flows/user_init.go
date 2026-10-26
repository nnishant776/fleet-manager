package flows

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// A user whose files are on a branch of their own (user/<user> in the secrets
// repository, see internal/secrets/users.go) keeps three things on their own
// machine: an age identity that decrypts those files, an SSH key that signs
// their commits, and the enrollment file that gives the maintainer the public
// halves. user init makes them; the other user commands use them.

// loginName is the name of the person running fleet, which is the default
// user of the user commands.
func loginName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

// ownUserName is the user whose branch a user command works on: the login
// name unless --user says otherwise.
func ownUserName() engine.Input {
	in := secrets.UserName()
	in.Prompt = "Which user are you?"
	in.Help = "The user's name: lowercase letters, digits and '-'; your login name by default"
	in.Default = func(*engine.Ctx) (string, error) { return loginName(), nil }
	return in
}

// identityInput is the user's passphrase-encrypted age identity.
var identityInput = engine.Input{
	Name:     "identity",
	Prompt:   "Your identity",
	Help:     "Your passphrase-encrypted age identity (age -p); ~/.fleet/user.age by default. Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY",
	Optional: true,
	Default: func(*engine.Ctx) (string, error) {
		if v := os.Getenv("FLEET_USER_IDENTITY"); v != "" {
			return v, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil
		}
		p := filepath.Join(home, ".fleet", "user.age")
		if fileExists(p) {
			return p, nil
		}
		return "", nil
	},
}

// signingKeyInput is the SSH key that signs the user's commits.
var signingKeyInput = engine.Input{
	Name:   "signing-key",
	Prompt: "Signing key",
	Help:   "The SSH private key that signs your commits to your branch; ~/.ssh/fleet_user_signing_key by default",
	Default: func(*engine.Ctx) (string, error) {
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".ssh", "fleet_user_signing_key"), err
	},
}

// enrollInput is the user's enrollment file.
var enrollInput = engine.Input{
	Name:   "enroll",
	Prompt: "Enrollment file",
	Help:   "Where your enrollment file is, or goes; ~/.fleet/<user>.enroll by default",
	Default: func(x *engine.Ctx) (string, error) {
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".fleet", x.Value("user")+".enroll"), err
	},
}

// UserInit makes the user's identity, signing key and enrollment file.
var UserInit = &engine.Flow{
	Path:  []string{"user", "init"},
	Title: "Set up your own keys for a branch of your own",
	Short: "Make your age identity, your signing key and the enrollment file to give the maintainer",
	Long: "Once per user: a passphrase-encrypted age identity, ~/.fleet/user.age, which decrypts your files in the\n" +
		"secrets repository; an SSH key, ~/.ssh/fleet_user_signing_key, that signs your commits to your branch,\n" +
		"user/<user>; and ~/.fleet/<user>.enroll, which holds the public halves of both (the age recipient and\n" +
		"the signing key) and nothing secret. Give the enrollment file to the maintainer, who runs fleet secrets\n" +
		"user-enroll. The identity is written straight into a passphrase-encrypted file and never touches the disk in\n" +
		"clear; keep a copy offline. Each step that is done already is skipped, so it is safe to run again.",
	Example: "  fleet user init\n  fleet user init --user alice",
	Inputs:  []engine.Input{ownUserName(), initIdentityInput(), signingKeyInput, enrollInput},
	Needs:   []string{"age", "age-keygen", "ssh-keygen"},
	Steps: []engine.Step{
		{
			Title: "Create your identity",
			Why:   "It decrypts your files, so it is written straight into a passphrase-encrypted file and never touches the disk in clear. age asks for the passphrase twice. Keep a copy offline.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("identity")) {
					return false, x.Value("identity") + " exists"
				}
				return true, ""
			},
			Do: fleet.Creates(engine.Cmd("sh", "-c", `umask 077 && mkdir -p "$(dirname "$1")" && age-keygen | age -p -o "$1"`, "sh", "{{.identity}}").
				Interactive().OK("written to {{.identity}}"),
				"your identity, {{.identity}}; keep a copy offline", "{{.identity}}"),
		},
		{
			Title: "Create your signing key",
			Why:   "An SSH key that signs every commit to your branch; hosts accept your commits only if it signed them. Give it a passphrase, and load it into your agent with ssh-add when you work.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("signing-key")) {
					return false, x.Value("signing-key") + " exists"
				}
				return true, ""
			},
			Do: fleet.Creates(engine.Cmd("ssh-keygen", "-t", "ed25519", "-a", "100", "-C", "{{.user}} fleet signing", "-f", "{{.signing-key}}").Interactive(),
				"your signing key, {{.signing-key}}", "{{.signing-key}}", "{{.signing-key}}.pub"),
		},
		{
			Title: "Write your enrollment file",
			Why: "The maintainer needs the public halves: your age recipient, derived from the identity (age asks for the passphrase once; nothing is written in clear), " +
				"and your signing key's type and material.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("enroll")) {
					return false, x.Value("enroll") + " exists"
				}
				return true, ""
			},
			Do: engine.Func(writeUserEnrollment),
		},
		{
			Title: "Give the enrollment file to the maintainer",
			Why:   "It holds public keys only, so any channel will do. The maintainer lists you on your hosts and moves your files to your branch.",
			Do: engine.Show("Give the maintainer {{.enroll}}. They run:\n" +
				"  fleet secrets user-enroll {{.user}} --from {{.user}}.enroll\n\n" +
				"Then your own commands work on your branch: fleet user password, fleet user keys, fleet user secret set."),
		},
	},
}

// writeUserEnrollment derives the identity's recipient and reads the signing
// key's public half, and writes them to the enrollment file.
func writeUserEnrollment(x *engine.Ctx) error {
	identity, pub := x.Value("identity"), x.Value("signing-key")+".pub"
	if x.Options.DryRun && (identity == "" || !fileExists(identity) || !fileExists(pub)) {
		x.UI.Skip("dry run: it would be derived from the new identity and key")
		return nil
	}
	if identity == "" {
		return errors.New("no identity: give --identity, or run this again without the earlier step skipped")
	}
	res, err := engine.Cmd("sh", "-c", `age -d "$1" | age-keygen -y`, "sh", "{{.identity}}").
		ReadOnly().Prompts().Capture("recipient").OK("{{.recipient}}").Run(x)
	if err != nil {
		return err
	}
	recipient := strings.TrimSpace(res.Stdout)
	if !userAgePattern.MatchString(recipient) {
		return errors.New("that is not an age recipient: " + res.Stdout)
	}
	b, err := os.ReadFile(pub)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return fmt.Errorf("%s does not hold an SSH public key", pub)
	}
	doc := "version: 1\nuser: " + x.Value("user") + "\nage_recipient: " + recipient + "\nsigning_key: " + fields[0] + " " + fields[1] + "\n"
	if ok, err := x.Approve("Write " + x.Value("enroll") + "?"); !ok || err != nil {
		return err
	}
	path := x.Value("enroll")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return err
	}
	x.UI.OK("wrote " + path)
	return nil
}

// initIdentityInput is identityInput for the command that makes it: the
// default is the place to make it at, whether or not it exists yet.
func initIdentityInput() engine.Input {
	in := identityInput
	in.Optional = false
	in.Help = "Where your passphrase-encrypted age identity (age -p) is, or goes; ~/.fleet/user.age by default"
	in.Default = func(*engine.Ctx) (string, error) {
		if v := os.Getenv("FLEET_USER_IDENTITY"); v != "" {
			return v, nil
		}
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".fleet", "user.age"), err
	}
	return in
}
