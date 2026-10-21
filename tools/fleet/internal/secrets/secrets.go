// Package secrets is the port of fleet-secrets (lib/apps/fleet-secrets.nix):
// the maintainer's side of the secrets repository. It runs the same yq, sops,
// git, mkpasswd and ssh-keygen commands as the shell tool, in the same order,
// and leaves the repository in the same state: identical fleet.yaml and
// .sops.yaml, the same files, encrypted to the same recipients, and the same
// commit messages. tests/fleet-secrets-parity runs both and compares.
package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// NamePattern is what host and user names may be: they end up in file names
// and in the regular expressions of .sops.yaml.
const NamePattern = `^[a-z0-9][a-z0-9-]*$`

// RepoInput is the secrets checkout.
var RepoInput = engine.Input{
	Name:   "repo",
	Prompt: "Secrets repository checkout",
	Help:   "The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory",
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

func in(x *engine.Ctx, rel string) string { return filepath.Join(x.Value("repo"), rel) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// cmd runs in the repository.
func cmd(name string, args ...string) *engine.Command {
	return engine.Cmd(name, args...).In("{{.repo}}")
}

// Open checks the repository is one, and that the tree is clean: every
// command commits exactly its own change.
func Open() engine.Step {
	return engine.Step{
		Title: "Open the secrets repository",
		Why:   "Each command commits exactly its own change, so the tree must have no uncommitted changes.",
		Do: engine.Checks(
			engine.Probe{
				What:   "fleet.yaml in {{.repo}}",
				Cmd:    cmd("test", "-f", "fleet.yaml"),
				Expect: func(string) (string, error) { return "", nil },
			},
			engine.Probe{
				// Untracked files are not a reason to refuse: the commands create some.
				What: "no uncommitted changes",
				Cmd:  cmd("git", "status", "--porcelain", "--untracked-files=no"),
				Expect: func(out string) (string, error) {
					if strings.TrimSpace(out) != "" {
						return "", errors.New("the working tree has uncommitted changes; commit or discard them first")
					}
					return "", nil
				},
			},
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

// Enrolled checks whether fleet.yaml lists the host; want says which answer
// lets the command go on, and why tells the person what to do otherwise.
func Enrolled(want bool, otherwise string) engine.Step {
	return engine.Step{
		Title: "Look up {{.host}} in fleet.yaml",
		Why:   "fleet.yaml lists every enrolled host with its age recipient.",
		Do: engine.Checks(engine.Probe{
			What: "{{.host}} in fleet.yaml",
			Cmd:  cmd("yq", ".hosts | has(strenv(h))", "fleet.yaml").Env("h={{.host}}"),
			Expect: func(out string) (string, error) {
				got := strings.TrimSpace(out) == "true"
				if got != want {
					return "", errors.New(otherwise)
				}
				if got {
					return "enrolled", nil
				}
				return "not enrolled yet", nil
			},
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

// SetRecipient records the host's recipient in fleet.yaml.
func SetRecipient() engine.Step {
	return engine.Step{
		Title: "Record {{.host}} in fleet.yaml",
		Why:   "The registry is the one thing the rules in .sops.yaml are rendered from.",
		Do: cmd("yq", "-i", ".hosts[strenv(h)] = strenv(r)", "fleet.yaml").Env("h={{.host}}", "r={{.recipient}}").
			Before(Restoring("fleet.yaml")),
	}
}

// Render writes .sops.yaml from fleet.yaml: literal recipients, no anchors,
// two rules per host (its own file and its users' files) and none per user.
func Render() engine.Step {
	return engine.Step{
		Title: "Render .sops.yaml from fleet.yaml",
		Why:   "Two rules per host, its own file and its users' files, each encrypted to that host and you only.",
		Do: engine.Func(func(x *engine.Ctx) error {
			res, err := cmd("yq", ".maintainer", "fleet.yaml").RunQuiet(x)
			if err != nil {
				return err
			}
			maintainer := strings.TrimSpace(res.Stdout)
			res, err = cmd("yq", ".hosts | keys | .[]", "fleet.yaml").RunQuiet(x)
			if err != nil {
				return err
			}
			hosts := strings.Fields(res.Stdout)
			if !strings.HasPrefix(maintainer, "age1") {
				return errors.New("fleet.yaml has no maintainer recipient")
			}
			var b bytes.Buffer
			b.WriteString("# Generated by fleet-secrets from fleet.yaml. Do not edit.\n")
			if len(hosts) == 0 {
				b.WriteString("creation_rules: []\n")
			} else {
				b.WriteString("creation_rules:\n")
			}
			for _, h := range hosts {
				res, err := cmd("yq", ".hosts[strenv(h)]", "fleet.yaml").Env("h=" + h).RunQuiet(x)
				if err != nil {
					return err
				}
				recipient := strings.TrimSpace(res.Stdout)
				for _, re := range []string{`^hosts/` + h + `\.yaml$`, `^users/[^/]+/` + h + `\.yaml$`} {
					fmt.Fprintf(&b, "  - path_regex: %s\n    key_groups:\n      - age: [\"%s\", \"%s\"]\n", re, maintainer, recipient)
				}
			}
			x.UI.Text(b.String())
			ok, err := x.Approve("Write it?")
			if !ok || err != nil {
				return err
			}
			if err := RestoreOnStop(x, "{{.repo}}", ".sops.yaml"); err != nil {
				return err
			}
			if err := os.WriteFile(in(x, ".sops.yaml"), b.Bytes(), 0o644); err != nil {
				return err
			}
			x.UI.OK(fmt.Sprintf("wrote .sops.yaml, %d rules for %d hosts", 2*len(hosts), len(hosts)))
			return nil
		}),
	}
}

// HostFiles are the files a host's rules cover that exist, in the shell's
// glob order.
func HostFiles(x *engine.Ctx, host string) []string {
	var files []string
	if exists(in(x, "hosts/"+host+".yaml")) {
		files = append(files, "hosts/"+host+".yaml")
	}
	users, _ := filepath.Glob(in(x, "users/*/"+host+".yaml"))
	for _, u := range users {
		rel, _ := filepath.Rel(x.Value("repo"), u)
		files = append(files, rel)
	}
	return files
}

// AllFiles are every encrypted file, hosts first, in the shell's glob order.
func AllFiles(x *engine.Ctx) []string {
	var files []string
	for _, pattern := range []string{"hosts/*.yaml", "users/*/*.yaml"} {
		matches, _ := filepath.Glob(in(x, pattern))
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
				rel, _ := filepath.Rel(x.Value("repo"), m)
				files = append(files, rel)
			}
		}
	}
	return files
}

// UpdateKeys re-encrypts files to the recipients their rules now name. files
// is computed when the step runs and kept as the value "files", one per line,
// for the commit.
func UpdateKeys(title, why string, files func(*engine.Ctx) []string) engine.Step {
	return engine.Step{
		Title: title,
		Why:   why,
		Do: engine.Func(func(x *engine.Ctx) error {
			fs := files(x)
			x.Values["files"] = strings.Join(fs, "\n")
			if len(fs) == 0 {
				x.UI.Skip("no files to re-encrypt")
				return nil
			}
			for _, f := range fs {
				if _, err := cmd("sops", "updatekeys", "-y", f).Before(Restoring(f)).Run(x); err != nil {
					return err
				}
			}
			return nil
		}),
	}
}

// CreateEmpty creates an empty encrypted file under the rules just rendered.
func CreateEmpty(title, rel string) engine.Step {
	return engine.Step{
		Title: title,
		Why:   "sops encrypts an empty document to the recipients the rules name for this path.",
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
			res, err := cmd("sops", "--encrypt", "--input-type", "yaml", "--output-type", "yaml",
				"--filename-override", rel, tmp.Name()).Quiet().OK("encrypted").Run(x)
			if err != nil {
				return fmt.Errorf("could not create %s: %w", rel, err)
			}
			if x.Options.DryRun {
				return nil
			}
			if err := RestoreOnStop(x, "{{.repo}}", rel); err != nil {
				return err
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

// Commit adds paths (templated; "{{.files}}" expands to one per line) and
// commits them with message.
func Commit(message string, paths ...string) engine.Step {
	return engine.Step{
		Title: "Commit",
		Why:   "Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.",
		Do: engine.Func(func(x *engine.Ctx) error {
			args := []string{"add"}
			for _, p := range paths {
				e, err := x.Expand(p)
				if err != nil {
					return err
				}
				args = append(args, strings.Fields(e)...)
			}
			added := args[1:]
			if _, err := cmd("git", args...).Before(func(x *engine.Ctx) error {
				return Unstage(x, "{{.repo}}", added)
			}).Run(x); err != nil {
				return err
			}
			msg, err := x.Expand(message)
			if err != nil {
				return err
			}
			_, err = cmd("git", "commit", "-q", "-m", msg).OK("committed: " + msg).After(func(x *engine.Ctx) error {
				Committed(x, "{{.repo}}", msg)
				return nil
			}).Run(x)
			return err
		}),
	}
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

// Set sets one key of an encrypted file from a value, passed on stdin so it
// never appears in a process listing.
func Set(file, key, value, note string) *engine.Command {
	return cmd("sops", "set", "--value-stdin", file, `["`+key+`"]`).StdinFrom(value).StdinNote(note).
		Before(Restoring(file))
}
