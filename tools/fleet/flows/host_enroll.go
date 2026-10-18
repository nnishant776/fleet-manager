package flows

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// HostEnroll brings a host that is already running into the secrets setup:
// what was done by hand for neerajpinew on 2026-10-18. Every step looks first
// and skips what is already done, so it also finishes a half-done enrollment.
var HostEnroll = &engine.Flow{
	Path:  []string{"host", "enroll"},
	Title: "Enroll a running host in the secrets setup",
	Short: "Enroll a running host: identity, enrollment, deploy key, first sync",
	Long: "For a host that already runs, and whose configuration in this repository sets\n" +
		"conf.secrets.repo.url. fleet logs in as its admin account through an agent that\n" +
		"holds one key only, switches the host to that configuration if it does not run\n" +
		"it yet, creates its identity, copies its enrollment file here and checks it,\n" +
		"enrolls it in the secrets repository and pushes, registers its deploy key\n" +
		"read-only, then syncs the host and checks it. Steps that are done already are\n" +
		"skipped, so it can be run again after a failure.",
	Example: "  fleet host enroll neerajpinew --key ~/.ssh/home_network_ssh_key --repo ~/Projects/fleet-secrets",
	Args:    []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Prompt: "Which host?", Help: "A host of the configuration (hosts/<name>)",
			Pattern: secrets.NamePattern, Choices: fleet.Hosts},
		{Name: "address", Prompt: "Address to reach it at", Help: "A name or IP address for SSH; defaults to the host's name",
			Default: func(x *engine.Ctx) (string, error) { return x.Value("host"), nil }},
		{Name: "admin", Prompt: "Account with sudo by SSH key", Help: "The account to log in as until the host has support; admin by default",
			Default: func(*engine.Ctx) (string, error) { return "admin", nil }},
		{Name: "user", Prompt: "Support account", Help: "The account for the steps after the switch; support by default, which needs nothing forwarded",
			Default: func(*engine.Ctx) (string, error) { return "support", nil }},
		{Name: "key", Prompt: "Key that logs in to that account and unlocks its sudo",
			Help: "A private key file; the only key the agent fleet starts holds and forwards",
			Default: func(*engine.Ctx) (string, error) {
				home, _ := os.UserHomeDir()
				if p := filepath.Join(home, ".ssh", "id_ed25519"); fileExists(p) {
					return p, nil
				}
				return "", nil
			}},
		secrets.RepoInput,
		secrets.RecoveryInput,
		{Name: "switch-to", Prompt: "Configuration to switch the host to, if it needs a switch",
			Help:     "A flake reference; defaults to the newest commit of " + fleet.Upstream,
			Optional: true, Default: func(*engine.Ctx) (string, error) { return "", nil }},
		{Name: "register-with", Prompt: "Command that registers the deploy key",
			Help:     "Run with the public key on stdin, instead of gh or the web page; mostly for tests",
			Optional: true, Default: func(*engine.Ctx) (string, error) { return "", nil }},
		{Name: "sync", Prompt: "Sync the host at the end and check it?",
			Help: "Sync the host at the end and run fleet host check (default true)", Bool: true,
			Default: func(*engine.Ctx) (string, error) { return "true", nil }},
	},
	Needs:   []string{"ssh", "ssh-agent", "ssh-add", "git", "yq", "sops", "age"},
	Cleanup: fleet.StopAgent,
	Steps: []engine.Step{
		{
			Title: "Start an agent that holds one key",
			Why:   "{{.admin}}'s sudo checks its key through the forwarded agent. Only this agent is forwarded, never yours, which holds your signing key.",
			Do:    engine.Func(fleet.StartAgent),
		},
		{
			Title: "Look at {{.host}}",
			Why:   "What is done already decides which of the next steps run.",
			Do:    engine.Func(lookAtHost),
		},
		{
			Title: "Switch {{.host}} to its fleet configuration",
			Why:   "It does not run a configuration with secrets yet. The switch runs as a service on the host, so it finishes even if this connection drops.",
			When:  unless("has_tooling", "it already runs a configuration with secrets"),
			Do:    engine.Func(switchHost),
		},
		{
			Title: "Point /etc/nixos at the configuration repository",
			Why:   "Its sync fetches origin, which should be the repository its configuration names.",
			When: func(x *engine.Ctx) (bool, string) {
				switch {
				case x.Value("has_checkout") != "true":
					return false, "there is no checkout yet; the first-boot clone makes one"
				case x.Value("config_url") == "":
					return false, "could not read conf.fleet.repo.url from " + x.Value("flake")
				case x.Value("origin") == x.Value("config_url"):
					return false, "it fetches " + x.Value("origin") + " already"
				}
				return true, ""
			},
			Do: fleet.OnAdmin("sudo -n git -C /etc/nixos remote set-url origin {{.config_url}}"),
		},
		{
			Title: "Create {{.host}}'s identity",
			Why:   "Its age key decrypts its secrets and its deploy key fetches them. Both are made on the host and never leave it.",
			When:  unless("has_identity", "it has one"),
			Do:    fleet.OnAdmin("sudo -n fleet-secrets-sync init --state /var/lib/fleet --host {{.host}}").OK("created under /var/lib/fleet"),
		},
		{
			Title: "Copy its enrollment file here",
			Why:   "It holds the two public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.",
			Do:    engine.Func(copyEnrollment),
		},
		{
			Title: "Enroll {{.host}} in the secrets repository",
			Why:   "fleet secrets host-enroll records its recipient, renders the rules and creates its empty file, each step shown.",
			When:  enrolledWithThisIdentity,
			Do:    engine.Func(runHostEnroll),
		},
		{
			Title: "Push the secrets repository",
			Why:   "{{.host}} fetches from GitHub; it accepts the commit only if it is signed by an allowed key.",
			When:  aheadOfUpstream,
			Do:    engine.Cmd("git", "push").In("{{.repo}}"),
		},
		{
			Title: "Register {{.host}}'s deploy key, read-only",
			Why:   "It is how {{.host}} fetches the private secrets repository. Read-only: a host never writes to it.",
			Do:    engine.Func(registerDeployKey),
		},
		{
			Title: "Sync {{.host}}",
			Why:   "Its first sync with secrets: fetch and verify them, then its configuration.",
			When:  wantSync,
			Do:    fleet.OnHost("sudo -n systemctl start config-auto-update"),
		},
		{
			Title: "Check {{.host}}",
			Why:   "fleet host check, as support with nothing forwarded: this also shows support works.",
			When:  wantSync,
			Do: engine.Cmd("fleet", "host", "check", "{{.host}}", "--address", "{{.address}}", "--flake", "{{.flake}}").
				ReadOnly().Interactive(),
		},
	},
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// unless skips a step when a fact from lookAtHost is true.
func unless(fact, reason string) func(*engine.Ctx) (bool, string) {
	return func(x *engine.Ctx) (bool, string) {
		if x.Value(fact) == "true" {
			return false, reason
		}
		return true, ""
	}
}

// lookAtHost records what is done already as values: has_tooling,
// has_identity, has_checkout, origin, has_support, config_url.
func lookAtHost(x *engine.Ctx) error {
	res, err := fleet.OnAdmin(`set -u
echo "login=$(id -un)"
sudo -n true 2>/dev/null && echo sudo=true || echo sudo=false
command -v fleet-secrets-sync >/dev/null && echo has_tooling=true || echo has_tooling=false
sudo -n test -f /var/lib/fleet/age.key && echo has_identity=true || echo has_identity=false
test -d /etc/nixos/.git && echo has_checkout=true || echo has_checkout=false
echo "origin=$(git -C /etc/nixos remote get-url origin 2>/dev/null)"
id support >/dev/null 2>&1 && echo has_support=true || echo has_support=false`).RunQuiet(x)
	if err != nil {
		return fmt.Errorf("could not log in as %s: %w", x.Value("admin"), err)
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			x.Values[k] = v
		}
	}
	if x.Value("sudo") != "true" {
		return fmt.Errorf("%s has no sudo with the key in the agent: is %s one of its sudoSshKeys?", x.Value("admin"), x.Value("key"))
	}
	report := func(fact, yes, no string) {
		if x.Value(fact) == "true" {
			x.UI.OK(yes)
		} else {
			x.UI.Skip(no)
		}
	}
	x.UI.OK("logged in as " + x.Value("login") + ", with sudo")
	report("has_tooling", "runs a configuration with secrets", "does not run a configuration with secrets yet")
	report("has_identity", "has its identity", "has no identity yet")
	report("has_checkout", "has a checkout in /etc/nixos, fetching "+x.Value("origin"), "has no checkout in /etc/nixos")
	report("has_support", "has the support account", "has no support account yet")
	// Where the configuration says the host syncs from, if the flake can tell.
	if url, err := engine.Silently(x, nil, "nix", "eval", "--raw", x.Value("flake")+"#nixosConfigurations."+x.Value("host")+".config.conf.fleet.repo.url"); err == nil {
		x.Values["config_url"] = url
	}
	return nil
}

func switchHost(x *engine.Ctx) error {
	ref := x.Value("switch-to")
	if ref == "" {
		out, err := engine.Silently(x, nil, "git", "ls-remote", "https://github.com/nnishant776/fleet-manager", "refs/heads/main")
		if err != nil || len(strings.Fields(out)) == 0 {
			return errors.New("could not read the newest commit of main; give --switch-to")
		}
		ref = fleet.Upstream + "/" + strings.Fields(out)[0]
	}
	x.Values["switch_ref"] = ref
	res, err := engine.Cmd("nix", "eval", "--raw", "{{.switch_ref}}#nixosConfigurations.{{.host}}.config.conf.secrets.repo.url").
		ReadOnly().Quiet().OK("its configuration there sets conf.secrets.repo.url").Run(x)
	if err != nil {
		return err
	}
	if !x.Options.DryRun && strings.TrimSpace(res.Stdout) == "" {
		return fmt.Errorf("%s does not set conf.secrets.repo.url for %s: add it, with conf.fleet.repo.url, the signers file and support's key, and push", ref, x.Value("host"))
	}
	// systemd-run gives the service a minimal PATH; nixos-rebuild needs the
	// system's (it runs `test`).
	_, err = fleet.OnAdmin(`sudo -n systemctl reset-failed fleet-enroll-switch 2>/dev/null; ` +
		`sudo -n systemd-run --wait --collect --unit=fleet-enroll-switch ` +
		`--setenv=PATH=/run/wrappers/bin:/run/current-system/sw/bin ` +
		`nixos-rebuild switch --flake {{.switch_ref}}#{{.host}}; s=$?; ` +
		`sudo -n journalctl -u fleet-enroll-switch -o cat -n 15 --no-pager; exit $s`).OK("switched").Run(x)
	return err
}

func copyEnrollment(x *engine.Ctx) error {
	res, err := fleet.OnAdmin(`sudo -n cat /var/lib/fleet/{{.host}}.enroll && sudo -n sha256sum /var/lib/fleet/{{.host}}.enroll`).
		RunQuiet(x)
	if err != nil {
		return err
	}
	doc, sumLine, ok := cutLast(res.Stdout)
	if !ok {
		return errors.New("unexpected answer from the host")
	}
	remote := strings.Fields(sumLine)[0]
	local := sha256.Sum256([]byte(doc))
	if hex.EncodeToString(local[:]) != remote {
		return fmt.Errorf("checksums differ: %s here, %s on the host", hex.EncodeToString(local[:])[:16], remote[:16])
	}
	// Next to the secrets checkout, whose .gitignore keeps *.enroll out.
	path := filepath.Join(x.Value("repo"), x.Value("host")+".enroll")
	x.Values["from"] = path
	x.Values["enroll_doc"] = doc
	for _, line := range strings.Split(doc, "\n") {
		if v, ok := strings.CutPrefix(line, "age_recipient: "); ok {
			x.Values["recipient"] = v
		}
		if v, ok := strings.CutPrefix(line, "deploy_pubkey: "); ok {
			x.Values["deploy_key"] = v
		}
	}
	if x.Options.DryRun {
		x.UI.Skip("dry run: checksums match (" + remote[:16] + "…), not copied to " + path)
		return nil
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return err
	}
	x.UI.OK("copied to " + path + ", checksum " + remote[:16] + "… on both sides")
	return nil
}

// cutLast splits off the last line.
func cutLast(s string) (string, string, bool) {
	s = strings.TrimRight(s, "\n")
	i := strings.LastIndex(s, "\n")
	if i < 0 {
		return "", "", false
	}
	return s[:i+1], s[i+1:], true
}

func enrolledWithThisIdentity(x *engine.Ctx) (bool, string) {
	got, _ := engine.Silently(x, []string{"h=" + x.Value("host")},
		"yq", `.hosts[strenv(h)] // ""`, filepath.Join(x.Value("repo"), "fleet.yaml"))
	switch {
	case got == "" || got == "null":
		return true, ""
	case got == x.Value("recipient"):
		return false, "it is enrolled already, with this identity"
	default:
		x.Values["enrolled_with"] = got
		return true, ""
	}
}

func runHostEnroll(x *engine.Ctx) error {
	if other := x.Value("enrolled_with"); other != "" {
		return fmt.Errorf("%s is enrolled with another identity (%s…); if it was reinstalled, run fleet secrets host-rekey %s --from %s",
			x.Value("host"), other[:12], x.Value("host"), x.Value("from"))
	}
	args := []string{"secrets", "host-enroll", "{{.host}}", "--from", "{{.from}}", "--repo", "{{.repo}}", "--recovery", "{{.recovery}}"}
	if x.Options.Yes {
		args = append(args, "--yes")
	}
	_, err := engine.Cmd("fleet", args...).ReadOnly().Interactive().OK("enrolled").Run(x)
	return err
}

func aheadOfUpstream(x *engine.Ctx) (bool, string) {
	out, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-list", "--count", "@{u}..HEAD")
	if err == nil && out == "0" {
		return false, "nothing to push"
	}
	return true, ""
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?$`)

func registerDeployKey(x *engine.Ctx) error {
	key := x.Value("deploy_key")
	if x.Options.DryRun && key == "" {
		key = "<deploy key>"
	}
	x.Values["deploy_key_line"] = key
	x.UI.Text(key)
	if cmd := x.Value("register-with"); cmd != "" {
		_, err := engine.Cmd("sh", "-c", cmd).StdinFrom("deploy_key_line").StdinNote("the deploy public key").Run(x)
		return err
	}
	origin, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "remote", "get-url", "origin")
	m := githubRemote.FindStringSubmatch(origin)
	_, noGh := x.LookPath("gh")
	if noGh == nil && m != nil {
		_, noGh = engine.Silently(x, nil, "gh", "auth", "status")
	}
	if noGh != nil || m == nil {
		x.UI.Note("Add it on the forge: the secrets repository → Settings → Deploy keys → Add deploy key, title " +
			x.Value("host") + ", with write access left unticked.")
		return engine.WaitFor(x, "Added it, read-only?")
	}
	x.Values["slug"] = m[1]
	keys, err := deployKeys(x, m[1])
	if err != nil {
		return err
	}
	body := strings.Join(strings.Fields(key)[:2], " ")
	for _, k := range keys {
		if k.Key == body {
			if !k.ReadOnly {
				return fmt.Errorf("the key is registered as %q but with write access: remove it and run this again", k.Title)
			}
			x.UI.OK("registered already, read-only, as " + k.Title)
			return nil
		}
	}
	tmp, err := os.CreateTemp("", "fleet-deploy-*.pub")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(key + "\n")
	tmp.Close()
	x.Values["key_file"] = tmp.Name()
	if _, err := engine.Cmd("gh", "repo", "deploy-key", "add", "{{.key_file}}", "--repo", "{{.slug}}", "--title", "{{.host}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	keys, err = deployKeys(x, m[1])
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.Key == body && k.ReadOnly {
			x.UI.OK("GitHub has it read-only, as " + k.Title)
			return nil
		}
	}
	return errors.New("GitHub does not list the key as read-only")
}

type deployKey struct {
	Title    string `json:"title"`
	Key      string `json:"key"`
	ReadOnly bool   `json:"read_only"`
}

func deployKeys(x *engine.Ctx, slug string) ([]deployKey, error) {
	res, err := engine.Cmd("gh", "api", "repos/"+slug+"/keys").RunQuiet(x)
	if err != nil {
		return nil, err
	}
	var keys []deployKey
	return keys, json.Unmarshal([]byte(res.Stdout), &keys)
}
