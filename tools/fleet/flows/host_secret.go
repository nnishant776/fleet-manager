package flows

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// HostSecret adds or changes one secret in a host's file.
var HostSecret = &engine.Flow{
	Path:  []string{"host", "secret"},
	Title: "Add or change a host secret",
	Short: "Add or change a host secret: encrypt, commit, push, and show how the configuration uses it",
	Long: "Encrypts one value into hosts/<host>.yaml of the secrets repository, to that\n" +
		"host and you only, then commits and pushes it. The value is typed hidden,\n" +
		"read from standard input, or, with --from-host, read off the host where it\n" +
		"is kept today, so it never appears on screen. Then it shows the lines that\n" +
		"make the host's configuration use it, and with --sync syncs the host and\n" +
		"checks the secret arrived.",
	Example: "  fleet host secret neerajpinew wifi_psk\n" +
		"  fleet host secret neerajpinew hotspot-wls2ccf67579ff5 --from-host /var/lib/hotspot/wls2ccf67579ff5\n" +
		"  printf %s \"$psk\" | fleet host secret pi-gw wifi_psk --value-stdin --yes",
	Args: []string{"host", "name"},
	Inputs: []engine.Input{
		secrets.RepoInput,
		secrets.RecoveryInput,
		knownHost(),
		{
			Name:    "name",
			Prompt:  "Name of the secret",
			Help:    "Its key in hosts/<host>.yaml, and its file in /run/secrets: letters, digits, - and _",
			Pattern: `^[A-Za-z0-9][A-Za-z0-9_-]*$`,
		},
		{
			Name:     "from-host",
			Prompt:   "Read the value off the host?",
			Help:     "A file on the host that holds the value today; empty to type it",
			Optional: true,
			Default:  func(*engine.Ctx) (string, error) { return "", nil },
		},
		{
			Name:   "value",
			Prompt: "Value of {{.name}} for {{.host}}",
			Help:   "Hidden as you type; trailing newlines are dropped",
			Secret: true,
			Skip:   func(x *engine.Ctx) bool { return x.Value("from-host") != "" },
		},
		{
			Name:    "address",
			Prompt:  "Address to reach it at",
			Help:    "A name or IP address for SSH; defaults to the host's name",
			Default: func(x *engine.Ctx) (string, error) { return x.Value("host"), nil },
		},
		{
			Name:    "user",
			Prompt:  "Account to log in as",
			Help:    "The account to log in as; support by default, which has sudo without a password",
			Default: func(*engine.Ctx) (string, error) { return "support", nil },
		},
		{
			Name:    "sync",
			Prompt:  "Sync the host afterwards and check the secret arrived?",
			Help:    "Sync the host after pushing and check the secret arrived (its configuration must use it already)",
			Bool:    true,
			Default: func(*engine.Ctx) (string, error) { return "false", nil },
		},
	},
	Needs: []string{"git", "yq", "sops", "age"},
	Steps: []engine.Step{
		secrets.Open(),
		secrets.Enrolled(true, "host '{{.host}}' is not enrolled; enroll it first (fleet host enroll, or fleet secrets host-enroll)"),
		{
			Title: "Read the current value off {{.host}}",
			Why:   "As {{.user}}, with nothing forwarded; the value is kept in memory and never shown.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("from-host") == "" {
					return false, "the value was typed or given on stdin"
				}
				return true, ""
			},
			Do: fleet.OnHost("sudo -n cat {{.from-host}}").ReadOnly().CaptureSecret("value").OK("read {{.from-host}}"),
		},
		secrets.Unlock(),
		{
			Title: "Encrypt it into hosts/{{.host}}.yaml",
			Why:   "Encrypted to {{.host}} and your recovery key only. An existing value of the same name is replaced.",
			Do: engine.Func(func(x *engine.Ctx) error {
				if x.Value("value") == "" && !x.Options.DryRun {
					return errors.New("the value is empty")
				}
				x.Values["value_json"] = secrets.JSON(x.Value("value"))
				_, err := secrets.Set("hosts/{{.host}}.yaml", "{{.name}}", "value_json", "the value").Run(x)
				return err
			}),
		},
		secrets.Commit("{{.host}}: {{.name}}", "hosts/{{.host}}.yaml"),
		{
			Title: "Push",
			Why:   "{{.host}} fetches it on its next sync, which checks the commit is signed by an allowed key.",
			Do:    engine.Cmd("git", "push").In("{{.repo}}"),
		},
		{
			Title: "Use it in {{.host}}'s configuration",
			Why:   "A host only decrypts secrets its configuration declares. Unless it already does, add this to hosts/{{.host}}/default.nix, commit and push:",
			Do: engine.Show(`sops.secrets."{{.name}}" = { };   # decrypted to /run/secrets/{{.name}} (root, 0400)` + "\n" +
				`# and point the option that needs it at config.sops.secrets."{{.name}}".path;` + "\n" +
				`# restartUnits = [ "<unit>.service" ] restarts a service when the value changes.`),
		},
		{
			Title: "Sync {{.host}}",
			Why:   "It fetches the new secrets commit and its configuration, and activation decrypts the secret.",
			When:  wantSync,
			Do:    fleet.OnHost("sudo -n systemctl start config-auto-update"),
		},
		{
			Title: "Check the secret arrived",
			Why:   "sops writes it to /run/secrets when the configuration declares it.",
			When:  wantSync,
			Do: engine.Checks(engine.Probe{
				What: "/run/secrets/{{.name}} on {{.host}}",
				Cmd:  fleet.OnHost(`sudo -n stat -L -c '%U %a' /run/secrets/{{.name}} 2>/dev/null || echo missing`),
				Expect: func(out string) (string, error) {
					out = strings.TrimSpace(out)
					if out == "missing" {
						return "", errors.New("not there: does the configuration declare sops.secrets.\"<name>\" yet, and was it pushed?")
					}
					return fmt.Sprintf("there, owner and mode %s", out), nil
				},
			}),
		},
	},
}

func wantSync(x *engine.Ctx) (bool, string) {
	if x.Value("sync") != "true" {
		return false, "not asked to (--sync)"
	}
	return true, ""
}
