package flows

import (
	"errors"
	"os"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// asSupport runs a script on the host as support with one given key only,
// forwarding nothing.
func asSupport(keyValue, script string) *engine.Command {
	return engine.Cmd("ssh",
		"-o", "ForwardAgent=no", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
		"-i", "{{."+keyValue+"}}",
		"support@{{.address}}", script,
	).Explain(fleet.SSHTrouble("support"))
}

// SupportKeyRotate replaces the key that opens the support account.
var SupportKeyRotate = &engine.Flow{
	Path:  []string{"support-key", "rotate"},
	Title: "Rotate the support key of a host",
	Short: "Replace the key that opens a host's support account, testing each step",
	Long: "support has sudo without a password, so its key is root on every host that\n" +
		"lists it. This makes a new key, ideally on a hardware security key (a touch,\n" +
		"and the PIN, on every login), shows the line to add to the host's\n" +
		"configuration next to the old key, syncs the host with the old key, tests the\n" +
		"new one, then has you remove the old line, syncs with the new key, and checks\n" +
		"the old key is refused. Optionally the new line accepts logins only from given\n" +
		"addresses (from=).",
	Example: "  fleet support-key rotate neerajpinew --old-key ~/.ssh/home_network_ssh_key --new-key ~/.ssh/fleet_support --kind hardware\n" +
		"  fleet support-key rotate neerajpinew --old-key ~/.ssh/home_network_ssh_key --new-key ~/.ssh/fleet_support --from 192.168.0.0/24",
	Args: []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Which host?", Help: "A host of the configuration (hosts/<name>)", Pattern: `^[A-Za-z0-9][A-Za-z0-9-]*$`, Choices: fleet.Hosts},
		{Name: "address", Prompt: "Address to reach it at", Help: "A name or IP address for SSH; defaults to the host's name",
			Default: func(x *engine.Ctx) (string, error) { return x.Value("host"), nil }},
		{Name: "old-key", Prompt: "The key support takes today", Help: "Its private key file"},
		{Name: "new-key", Prompt: "Where the new key goes", Help: "A private key file; made unless it exists"},
		{Name: "kind", Prompt: "Kind of new key", Help: "hardware (a FIDO2 security key: touch and PIN) or software (a passphrase); hardware by default",
			Pattern: `^(hardware|software)$`, Default: func(*engine.Ctx) (string, error) { return "hardware", nil }},
		{Name: "from", Prompt: "Accept the new key only from", Help: "Addresses or networks for sshd's from=, such as 192.168.0.0/24; empty for anywhere",
			Optional: true, Default: func(*engine.Ctx) (string, error) { return "", nil }},
	},
	Needs: []string{"ssh", "ssh-keygen"},
	Steps: []engine.Step{
		{
			Title: "Make the new key",
			Why:   "A hardware key keeps the private half on the device: every login needs a touch, and its PIN. A software key gets a passphrase.",
			When: func(x *engine.Ctx) (bool, string) {
				if fileExists(x.Value("new-key")) {
					return false, x.Value("new-key") + " exists"
				}
				return true, ""
			},
			Do: engine.Func(func(x *engine.Ctx) error {
				args := []string{"-t", "ed25519", "-a", "100", "-C", "fleet support", "-f", "{{.new-key}}"}
				if x.Value("kind") == "hardware" {
					args = []string{"-t", "ed25519-sk", "-O", "resident", "-O", "verify-required",
						"-O", "application=ssh:fleet-support", "-C", "fleet support", "-f", "{{.new-key}}"}
				}
				_, err := fleet.Creates(engine.Cmd("ssh-keygen", args...).Interactive().OK("made {{.new-key}}"),
					"the new key, {{.new-key}}", "{{.new-key}}", "{{.new-key}}.pub").Run(x)
				return err
			}),
		},
		{
			Title: "Add the new key next to the old one",
			Why:   "Both are accepted for now, so the host can be reached whichever way the next steps go.",
			Do: engine.Func(func(x *engine.Ctx) error {
				pub, err := os.ReadFile(x.Value("new-key") + ".pub")
				if err != nil && !x.Options.DryRun {
					return err
				}
				line := strings.TrimSpace(string(pub))
				if line == "" {
					line = "<the new public key>"
				}
				if from := x.Value("from"); from != "" {
					line = `from=\"` + from + `\" ` + line
				}
				x.Values["new_line"] = line
				x.Say("In hosts/{{.host}}/default.nix, under conf.fleet.support.sshKeys, add:")
				x.UI.Text(`  "` + line + `"`)
				x.UI.Text("then commit, push to main (merging locally), and come back.")
				return engine.WaitFor(x, "Pushed?")
			}),
		},
		{
			Title: "Sync {{.host}} with the old key",
			Why:   "It picks up the configuration that accepts both keys.",
			Do:    fleet.StillRunning(asSupport("old-key", "sudo -n systemctl start config-auto-update"), "the sync", "config-auto-update", "support"),
		},
		{
			Title: "Test the new key",
			Why:   "With the new key only and nothing else offered: it must log in, and sudo must work.",
			Do: engine.Checks(engine.Probe{
				What: "support on {{.host}} with the new key",
				Cmd:  asSupport("new-key", "sudo -n id -u"),
				Expect: func(out string) (string, error) {
					if strings.TrimSpace(out) != "0" {
						return "", errors.New("sudo did not give root: " + out)
					}
					return "logged in, and sudo gives root", nil
				},
			}),
		},
		{
			Title: "Remove the old key",
			Why:   "Until it is gone, whoever holds it is still root on {{.host}}.",
			Do: engine.Func(func(x *engine.Ctx) error {
				x.Say("In hosts/{{.host}}/default.nix, remove the old key from conf.fleet.support.sshKeys,")
				x.UI.Text("commit, push to main (merging locally), and come back.")
				return engine.WaitFor(x, "Pushed?")
			}),
		},
		{
			Title: "Sync {{.host}} with the new key",
			Why:   "It picks up the configuration without the old key.",
			Do:    fleet.StillRunning(asSupport("new-key", "sudo -n systemctl start config-auto-update"), "the sync", "config-auto-update", "support"),
		},
		{
			Title: "Check the old key is refused",
			Why:   "The rotation is done only when the old key no longer opens support.",
			Do: engine.Func(func(x *engine.Ctx) error {
				_, err := asSupport("old-key", "true").ReadOnly().Quiet().RunQuiet(x)
				switch {
				case x.Options.DryRun:
				case err == nil:
					return errors.New("the old key still logs in: is it still listed, or was the change not pushed and synced?")
				default:
					x.UI.OK("the old key is refused")
				}
				return nil
			}),
		},
	},
}
