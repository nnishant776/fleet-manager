package flows

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/secrets"
)

// Once an adopted machine's folder is in the inventory, fleetmgr host adopt
// brings the machine under the fleet as host enroll brings a host: its
// identity and its branch of the secrets, its keys registered, then a trial
// switch. Before the switch, the passwords and keys of the accounts its
// configuration takes from the secrets are stored there, as the machine has
// them, so that everyone keeps theirs, and so are support's.

// machineSwitchInputs are the inputs of that half.
var machineSwitchInputs = []engine.Input{
	{Name: "support-keys", Prompt: "File with support's login keys", Optional: true, Skip: onlyFor("machine"),
		Help: "SSH public keys, one per line, that log in to the support account on the host once it runs the fleet's configuration; " +
			"needed unless host/<host> of the secrets repository holds them already"},
	withSkip(trialMinutesInput, onlyFor("machine")),
	{Name: "switch-to", Prompt: "Configuration to switch the host to", Optional: true, Skip: onlyFor("machine"),
		Help: "A flake reference; by default the commit of the inventory fleetmgr install prep would install"},
	{Name: "register-with", Prompt: "Command that registers the deploy keys", Optional: true, Skip: onlyFor("machine"),
		Help: "Run with each public key on stdin, instead of gh or the web page; mostly for tests"},
	{Name: "sync", Prompt: "Sync the host at the end and check it?", Bool: true, Skip: onlyFor("machine"),
		Help:    "Sync the host at the end and run fleetmgr host check (default true)",
		Default: func(*engine.Ctx) (string, error) { return "true", nil }},
}

// machineSwitchSteps are that half's steps, with host enroll's own.
var machineSwitchSteps = joinSteps(
	enrollIdentitySteps,
	[]engine.Step{
		secrets.Open(true),
		skipInDryRun(secrets.Unlock()),
		{
			Title: "Store the accounts' passwords and keys",
			Why: "Each account the host's configuration takes its password or login keys from the secrets for gets them from the machine as they are: " +
				"its password hash, never shown, and the keys in its authorized_keys. Everyone keeps their password and keys.",
			When: needsSwitch,
			Do:   engine.Func(storeAccountSecrets),
		},
		{
			Title: "Store support's keys",
			Why:   "The support account logs in with them once the host runs the fleet's configuration, and fleetmgr confirms the new system with them.",
			When:  supportKeysNeeded,
			Do:    engine.Func(storeSupportKeys),
		},
		{
			Title: "Push the secrets",
			Why:   "{{.host}} fetches them before its switch, and accepts the commits only if a key it trusts signed them. What the steps above left unpushed goes now.",
			When:  needsSwitch,
			Do:    engine.Func(pushOwnerSecrets),
		},
	},
	enrollChecksSteps,
	trialSwitchSteps(needsSwitch, engine.Step{
		Title: "List what stops running on {{.host}}",
		Why: "A service that runs on the machine now and has no unit in the new system stops at the switch: the network, SSH or a database should not go unnoticed. " +
			"Nothing changes here; with such services, it asks before going on.",
		Do: engine.Func(listStoppingServices),
	}),
	enrollFinishSteps,
)

// runSecretsFor runs a secrets command's steps for a user on this host.
func runSecretsFor(x *engine.Ctx, f *engine.Flow, user string, flags map[string]string) error {
	flags["repo"], flags["recovery"] = x.Value("repo"), ""
	flags["user"], flags["host"] = user, x.Value("host")
	return x.RunSteps(f.Inputs, flags, secretsBody(f))
}

// declaredAccount is what an account of the host's configuration takes from
// the secrets.
type declaredAccount struct {
	Password bool `json:"password"`
	Keys     bool `json:"keys"`
}

func storeAccountSecrets(x *engine.Ctx) error {
	res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.users.accounts",
		"--apply", "builtins.mapAttrs (_: a: { password = a.secrets.password; keys = a.secrets.authorizedKeys; })").
		ReadOnly().Quiet().OK("read which accounts take their password or keys from the secrets").Run(x)
	if err != nil {
		return fmt.Errorf("reading %s's accounts: %w: %s", x.Value("host"), err, strings.TrimSpace(res.Stderr))
	}
	if x.Options.DryRun && strings.TrimSpace(res.Stdout) == "" {
		return nil
	}
	var accounts map[string]declaredAccount
	if err := json.Unmarshal([]byte(res.Stdout), &accounts); err != nil {
		return fmt.Errorf("%s's accounts: %w", x.Value("host"), err)
	}
	var names []string
	for n := range accounts {
		if n != x.Value("user") && (accounts[n].Password || accounts[n].Keys) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		a := accounts[name]
		if !namePattern.MatchString(name) {
			return fmt.Errorf("the account %q cannot be a user in the secrets repository", name)
		}
		if x.Options.DryRun {
			x.UI.Skip("dry run: would store " + name + "'s password hash and keys as the machine has them")
			continue
		}
		if a.Password {
			hash := x.Value("adopt_hash_" + name)
			if hash == "" {
				if hash, err = readShadowHash(x, name); err != nil {
					return err
				}
			}
			if hash == "" {
				return fmt.Errorf("the configuration takes %s's password from the secrets, and %s has none on the machine: have them set one with passwd, or set secrets.password = false for the account", name, name)
			}
			if err := runSecretsFor(x, SecretsUserPassword, name, map[string]string{"hash": hash}); err != nil {
				return err
			}
		}
		if a.Keys {
			keys := x.Value("adopt_keys_" + name)
			if keys == "" {
				home, err := accountHome(x, name)
				if err != nil {
					return err
				}
				lines, err := readAuthorizedKeys(x, name, home)
				if err != nil {
					return err
				}
				keys = strings.Join(lines, "\n")
			}
			if keys == "" {
				return fmt.Errorf("the configuration takes %s's login keys from the secrets, and %s has none on the machine", name, name)
			}
			if err := storeKeysFor(x, name, keys); err != nil {
				return err
			}
		}
	}
	x.Kept("adopt secrets", "the accounts' password hashes and keys are in the secrets repository {{.repo}} and stay there; run this again to go on")
	return nil
}

// accountHome reads an account's home on the machine.
func accountHome(x *engine.Ctx, name string) (string, error) {
	res, err := fleetmgr.OnAdmin("getent passwd " + name).ReadOnly().Quiet().Run(x)
	f := strings.Split(strings.TrimSpace(res.Stdout), ":")
	if err != nil || len(f) < 7 || f[0] != name || !homePattern.MatchString(f[5]) {
		return "", fmt.Errorf("%s has no account %s to read keys from", x.Value("host"), name)
	}
	return f[5], nil
}

func storeKeysFor(x *engine.Ctx, user, keys string) error {
	dir, err := os.MkdirTemp("", "fleetmgr-adopt-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(file, []byte(keys+"\n"), 0o600); err != nil {
		return err
	}
	return runSecretsFor(x, SecretsUserKeys, user, map[string]string{"authorized-keys": file})
}

// supportKeysNeeded is whether support's keys are to be stored: they are
// given, or the host's branch has none yet.
func supportKeysNeeded(x *engine.Ctx) (bool, string) {
	if ok, why := needsSwitch(x); !ok {
		return false, why
	}
	if x.Value("support-keys") != "" {
		return true, ""
	}
	if ref := secrets.BranchRef(x, x.Value("host")); ref != "" {
		if _, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "cat-file", "-e",
			ref+":users/"+x.Value("user")+"/"+x.Value("host")+".yaml"); err == nil {
			return false, "host/" + x.Value("host") + " holds them already, and --support-keys gives none"
		}
	}
	return true, ""
}

func storeSupportKeys(x *engine.Ctx) error {
	file := x.Value("support-keys")
	if file == "" {
		return fmt.Errorf("host/%s holds no keys for %s, who logs in to confirm the new system: give them with --support-keys", x.Value("host"), x.Value("user"))
	}
	keys, err := keyLinesOf(file)
	if err != nil {
		return err
	}
	if x.Options.DryRun {
		x.UI.Skip(fmt.Sprintf("dry run: would store the %d key(s) in %s for %s", len(keys), file, x.Value("user")))
		return nil
	}
	return storeKeysFor(x, x.Value("user"), strings.Join(keys, "\n"))
}

// stoppingScript lists, for the new system given, the services running now
// that it has no unit for, a template's instances by their template.
const stoppingScript = `new=$1
systemctl list-units --type=service --state=running --no-legend --plain | while read -r unit _; do
  case "$unit" in user@*|session-*|run-*|fleetmgr-*) continue ;; esac
  [ -e "$new/etc/systemd/system/$unit" ] && continue
  case "$unit" in *@*) [ -e "$new/etc/systemd/system/${unit%%@*}@.service" ] && continue ;; esac
  echo "gone=$unit"
done`

func listStoppingServices(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing was built, so there is nothing to compare")
		return nil
	}
	x.Values["stopping_script"] = stoppingScript
	res, err := fleetmgr.OnAdmin("sh -s -- {{.new_system}}").StdinFrom("stopping_script").StdinNote("the comparison").
		ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	var gone []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if u, ok := strings.CutPrefix(strings.TrimSpace(line), "gone="); ok && serviceName.MatchString(u) {
			gone = append(gone, u)
		}
	}
	if len(gone) == 0 {
		x.UI.OK("every service that runs now has a unit in the new system")
		return nil
	}
	x.UI.Text("These run on " + x.Value("host") + " now and have no unit in the new system, so they stop at the switch:\n  " + strings.Join(gone, "\n  "))
	x.UI.Note("If one matters, give the host's configuration what runs it, publish, and run this again. The trial brings the old system back only if support cannot log in.")
	ok, err := x.Approve("Switch all the same?")
	if err != nil || !ok {
		return err
	}
	return nil
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]+\.service$`)
