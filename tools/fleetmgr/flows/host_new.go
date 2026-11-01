package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/secrets"
)

// HostNew sets up a host the inventory does not have, before it is installed,
// and enrolls it while the installer waits. Part 1 writes the host's folder
// from what it is told, commits, pushes and publishes it, and prints the line
// for the installer; part 2 takes the enrollment file the installer shows and
// sets the host up in the secrets repository. Every step looks first and
// skips what is done, so a second run goes on from where the first stopped.
var HostNew = &engine.Flow{
	Path:  []string{"host", "new"},
	Title: "Set up a new host in the inventory, and enroll it as it is installed",
	Short: "Write a new host's folder, publish it, print the install line, and set up its secrets from the enrollment file the installer shows",
	Long: "fleetmgr host new sets up a machine that is to be installed from a private inventory, in one guided run.\n" +
		"\n" +
		"Part 1, before the machine is installed: it asks what the host is (its platform, role and machine type,\n" +
		"how it boots, its disk and whether to encrypt it, its network, timezone and locale, and its accounts), each\n" +
		"answer also a flag. It writes the host's folder in the inventory: one default.nix, with the disk layout on,\n" +
		"which the installer lays out. It checks the host evaluates, commits, pushes and publishes its branch, then\n" +
		"checks the commits the install takes and prints the line to run on the machine booted from the NixOS\n" +
		"installer, as fleetmgr install prep does.\n" +
		"\n" +
		"Part 2, when the installer shows the host's enrollment file: on a terminal it waits for you to copy the file\n" +
		"to the path it names; a later run takes it with --from. It enrolls the host in the secrets repository,\n" +
		"stores support's keys (--support-keys), each account's password (asked twice and hashed here; --no-password\n" +
		"for an account that logs in by key only) and login keys (--keys name=file), pushes the host's branch, and\n" +
		"registers the host's deploy key on the secrets repository and its configuration key on the inventory, both\n" +
		"read-only. Then you press Enter on the installer, which installs.",
	Example: "  fleetmgr host new desk1 --flake inventory --repo secrets\n" +
		"  fleetmgr host new desk1 --flake inventory --repo secrets --platform x86_64-linux --role developer \\\n" +
		"      --machine-type desktop --boot uefi --disk /dev/nvme0n1 --network networkmanager \\\n" +
		"      --account 'alice:Alice Liddell:admin' --support-keys ~/support.pub --from ~/desk1.enroll",
	Args: []string{"host"},
	Inputs: []engine.Input{
		fleetmgr.FlakeInput,
		{Name: "host", Prompt: "Name of the new host", Pattern: secrets.NamePattern,
			Help: "Its name in the inventory and its hostname: lowercase letters, digits and -"},
		{Name: "platform", Prompt: "Its platform", Pattern: `^(x86_64-linux|aarch64-linux)$`,
			Help: "The platform, nixpkgs.hostPlatform: x86_64-linux or aarch64-linux", Choices: fixedChoices("x86_64-linux", "aarch64-linux")},
		{Name: "role", Prompt: "Its role", Pattern: `^(` + strings.Join(adoptRoles, "|") + `)$`,
			Help: "The role, conf.role: " + strings.Join(adoptRoles, ", "), Choices: fixedChoices(adoptRoles...)},
		{Name: "machine-type", Prompt: "Its machine type", Pattern: `^(` + strings.Join(adoptMachineTypes, "|") + `)$`,
			Help: "The machine type, conf.machineType: " + strings.Join(adoptMachineTypes, ", "), Choices: fixedChoices(adoptMachineTypes...)},
		{Name: "boot", Prompt: "How it boots", Pattern: `^(uefi|bios)$`,
			Help: "How it boots: uefi, with systemd-boot, or bios, with GRUB on its disk", Choices: fixedChoices("uefi", "bios")},
		{Name: "disk", Prompt: "Its disk", Pattern: `^/dev/[A-Za-z0-9/_.:-]+$`,
			Help: "The disk the installer lays out, such as /dev/nvme0n1; os-install -d can name another at install time, except on a BIOS host, whose GRUB installs to this one"},
		{Name: "encrypt", Prompt: "Encrypt its disk?", Bool: true,
			Help:    "Whether LUKS2 encrypts its disk, with a passphrase asked at install time (default true; --encrypt=false for a VM)",
			Default: func(*engine.Ctx) (string, error) { return "true", nil }},
		{Name: "network", Prompt: "Its network backend", Pattern: `^(networkmanager|networkd)$`,
			Help: "The network backend: networkmanager or networkd", Choices: fixedChoices("networkmanager", "networkd")},
		{Name: "timezone", Prompt: "Its timezone", Pattern: adoptTimezone.String(),
			Help: "Such as Europe/Berlin; by default the one the inventory's hosts share", Default: sharedHostValue("conf.host.timezone")},
		{Name: "locale", Prompt: "Its locale", Pattern: adoptLocale.String(),
			Help: "Such as en_US.UTF-8; by default the one the inventory's hosts share", Default: sharedHostValue("conf.host.locale")},
		{Name: "account", Prompt: "Its accounts", Many: true, Pattern: newAccountPattern.String(),
			Help: "An account, as name:Full Name, or name:Full Name:admin for an administrator; given once per account"},
		{Name: "state-version", Prompt: "Its system.stateVersion", Optional: true, Pattern: `^[0-9]{2}\.[0-9]{2}$`,
			Help: "The NixOS release it is installed with; by default the release the inventory's hosts build with"},
		{Name: "secrets-address", Prompt: "The secrets repository's address", Optional: true,
			Help: "The secrets repository's address, conf.secrets.repo.url, for the host; by default the one the inventory's hosts share"},
		secrets.RepoInput, secrets.RecoveryInput,
		{Name: "from", Prompt: "Its enrollment file", Optional: true,
			Help: "The enrollment file the installer shows, copied here; without it, a run on a terminal waits for it"},
		{Name: "user", Prompt: "Support account", Help: "The account the maintainer logs in to the host as; support by default",
			Default: func(*engine.Ctx) (string, error) { return "support", nil }},
		{Name: "support-keys", Prompt: "File with support's login keys", Optional: true,
			Help: "The SSH public keys, one per line, that log in to the support account; needed unless host/<host> of the secrets repository holds them already"},
		{Name: "no-password", Prompt: "Accounts without a password", Many: true, Optional: true, Pattern: secrets.NamePattern,
			Help: "An account that logs in by key only, and gets no password; given once per account"},
		{Name: "keys", Prompt: "Accounts' login keys", Many: true, Optional: true, Pattern: `^[a-z][a-z0-9-]*=.+$`,
			Help: "An account's login keys, as name=file: the SSH public keys, one per line; given once per account"},
		{Name: "register-with", Prompt: "Command that registers the deploy keys", Optional: true,
			Help: "Run with each public key on stdin, instead of gh or the web page; mostly for tests"},
	},
	Needs: []string{"git", "nix", "sops", "age", "ssh-keygen", "mkpasswd"},
	Steps: joinSteps(newHostPart1, InstallPrep.Steps, newHostPart2),
}

// newAccountPattern is an account as --account gives it.
var newAccountPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*:[^:]+(:admin)?$`)

// newHostSubject is the subject of the commit that adds a host this command
// wrote, by which a second run knows its own.
func newHostSubject(host string) string {
	return "feat(hosts): " + host + ", set up by fleetmgr host new"
}

var newHostPart1 = []engine.Step{
	{
		Title: "Check the inventory",
		Why: "The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. " +
			"Its layout says where hosts go. A host it has already is refused, unless an earlier run of this command added it.",
		Required: true,
		Do:       engine.Func(checkNewHostTarget),
	},
	{
		Title: "Write {{.host}}'s folder into the inventory",
		Why:   "One file, default.nix, from your answers, with the disk layout on: the installer lays the disk out from it. It is shown first, and written only once you agree.",
		When:  newHostWrites,
		Do:    engine.Func(writeNewHostFolder),
	},
	{
		Title: "Check it evaluates in the inventory",
		Why:   "The host is evaluated as the hosts will see it, and every host still evaluates. An evaluation that fails says why, and leaves the folder for you to change.",
		When:  unless("adopt_in_inventory", "it is in the inventory already"),
		Do:    engine.Func(checkAdopted),
	},
	{
		Title: "Commit it",
		Why:   "One commit that adds the host's folder. The repository's own git settings sign it.",
		When:  unless("adopt_in_inventory", "it is in the inventory already"),
		Do:    engine.Func(commitNewHost),
	},
	{
		Title: "Push it",
		Why:   "Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first.",
		When:  adoptAhead,
		Do:    engine.Func(pushImport),
	},
	{
		Title: "Publish its branch",
		Why:   "The host fetches its own branch, host/{{.host}}, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.",
		When: func(x *engine.Ctx) (bool, string) {
			if x.Value("imp_host_branches") != "true" {
				return false, "the inventory does not set hostBranches: its hosts fetch main"
			}
			return true, ""
		},
		Do: engine.Func(publishImport),
	},
}

var newHostPart2 = []engine.Step{
	{
		Title: "Take {{.host}}'s enrollment file",
		Why: "The installer makes the host's identity first and shows its enrollment file: the public halves only, its age recipient and its two deploy keys. " +
			"It is checked to be {{.host}}'s before anything is done with it.",
		Required: true,
		Do:       engine.Func(takeNewHostEnrollment),
	},
	secrets.Open(true),
	skipInDryRun(secrets.Unlock()),
	{
		Title: "Enroll {{.host}} in the secrets repository",
		Why:   "fleetmgr secrets host-enroll creates its branch, host/{{.host}}, with its empty file encrypted to it and to your recovery recipient, each step shown.",
		When:  enrolledWithThisIdentity,
		Do:    engine.Func(runHostEnroll),
	},
	{
		Title: "Store support's keys",
		Why:   "The support account logs in with them once the host runs, which is how you reach it.",
		When:  newSupportKeysNeeded,
		Do:    engine.Func(storeSupportKeys),
	},
	{
		Title: "Store the accounts' passwords",
		Why:   "Each account's password is asked for twice, hashed here, and only the hash is stored, encrypted to the host. An account given --no-password gets none.",
		Do:    engine.Func(storeNewHostPasswords),
	},
	{
		Title: "Store the accounts' login keys",
		Why:   "The keys --keys gives, one file per account, stored as each account's authorized keys.",
		When: func(x *engine.Ctx) (bool, string) {
			if x.Value("keys") == "" {
				return false, "--keys gives none"
			}
			return true, ""
		},
		Do: engine.Func(storeNewHostKeys),
	},
	{
		Title: "Push host/{{.host}}",
		Why:   "The installer fetches the host's secrets from it, and the host accepts the commits only if a key it trusts signed them.",
		When:  aheadOfUpstream,
		Do:    secrets.PushBranch(),
	},
	{
		Title: "Register {{.host}}'s deploy key, read-only",
		Why:   "It is how {{.host}} fetches the private secrets repository. Read-only: a host never writes to it.",
		Do:    engine.Func(registerDeployKey),
	},
	{
		Title: "Register {{.host}}'s configuration key, read-only",
		Why:   "Its configuration comes over SSH, from a private repository, and this key is how {{.host}} fetches it. Read-only, like the deploy key.",
		When: func(x *engine.Ctx) (bool, string) {
			if !sshURL(x.Value("config_url")) {
				return false, "its configuration repository needs no key: " + x.Value("config_url") + " is not an SSH address"
			}
			return true, ""
		},
		Do: engine.Func(registerConfigKey),
	},
	{
		Title: "Next",
		Why:   "What is left to do on the installer, and after the first boot.",
		Do:    engine.Func(showNewHostNext),
	},
}

// fixedChoices is a list to choose from that does not depend on anything.
func fixedChoices(options ...string) func(*engine.Ctx) ([]string, error) {
	return func(*engine.Ctx) ([]string, error) { return options, nil }
}

// sharedHostValue is the value of a host option every host of the inventory
// shares, or none.
func sharedHostValue(option string) func(*engine.Ctx) (string, error) {
	return func(x *engine.Ctx) (string, error) {
		res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations",
			"--apply", "cs: builtins.mapAttrs (_: c: c.config."+option+") cs").ReadOnly().Quiet().Run(x)
		if err != nil {
			return "", nil
		}
		var values map[string]string
		if json.Unmarshal([]byte(res.Stdout), &values) != nil {
			return "", nil
		}
		common := ""
		for _, v := range values {
			switch {
			case common == "":
				common = v
			case common != v:
				return "", nil
			}
		}
		return common, nil
	}
}

// newHostWrites says whether the folder is still to be written.
func newHostWrites(x *engine.Ctx) (bool, string) {
	switch {
	case x.Value("adopt_in_inventory") == "true":
		return false, "it is in the inventory already"
	case x.Value("adopt_written") == "true":
		return false, "an earlier run wrote it, and it is checked again as it is"
	}
	return true, ""
}

// checkNewHostTarget checks the inventory as host adopt does, and refuses a
// host it has that this command did not add.
func checkNewHostTarget(x *engine.Ctx) error {
	if err := checkAdoptTarget(x); err != nil {
		return err
	}
	if x.Value("adopt_in_inventory") != "true" {
		return nil
	}
	added, _ := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "log", "--diff-filter=A", "--format=%s", "--",
		x.Value("imp_paths")+"/default.nix")
	lines := strings.Split(strings.TrimSpace(added), "\n")
	if lines[len(lines)-1] != newHostSubject(x.Value("host")) {
		return fmt.Errorf("%s is in the inventory already, and fleetmgr host new did not add it: pick another name, or bring a running machine in with fleetmgr host enroll or host adopt", x.Value("host"))
	}
	x.UI.OK("an earlier run added it: part 1 is done, and it goes on from there")
	return nil
}

// newHostAccounts are the accounts --account gives, with what --no-password
// and --keys say of each.
func newHostAccounts(x *engine.Ctx) ([]machineAccount, error) {
	noPassword := lines(x.Value("no-password"))
	keyed := map[string]bool{}
	for _, k := range lines(x.Value("keys")) {
		name, _, _ := strings.Cut(k, "=")
		keyed[name] = true
	}
	var accounts []machineAccount
	seen := map[string]bool{}
	for _, a := range lines(x.Value("account")) {
		parts := strings.Split(a, ":")
		name, full := parts[0], strings.TrimSpace(parts[1])
		if seen[name] {
			return nil, fmt.Errorf("the account %s is given twice", name)
		}
		if name == x.Value("user") {
			return nil, fmt.Errorf("%s is the support account, which the fleet sets up itself", name)
		}
		seen[name] = true
		accounts = append(accounts, machineAccount{
			Name: name, Full: full, Privileged: len(parts) == 3,
			Password: !slices.Contains(noPassword, name), Keys: keyed[name],
		})
	}
	if len(accounts) == 0 {
		return nil, errors.New("no account: give one with --account name:Full Name[:admin]")
	}
	for _, n := range noPassword {
		if !seen[n] {
			return nil, fmt.Errorf("--no-password names %s, which --account does not give", n)
		}
	}
	for n := range keyed {
		if !seen[n] {
			return nil, fmt.Errorf("--keys names %s, which --account does not give", n)
		}
	}
	return accounts, nil
}

// lines are the values of an input given more than once.
func lines(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, "\n")
}

// newHostRelease is the release the host is installed with: --state-version,
// else the one the inventory's hosts build with, else fleet-manager's default.
func newHostRelease(x *engine.Ctx) (string, error) {
	if v := x.Value("state-version"); v != "" {
		return v, nil
	}
	res, err := engine.Cmd("nix", "eval", "--raw", "{{.flake}}#nixosConfigurations",
		"--apply", `cs: let names = builtins.attrNames cs; in if names == [ ] then "" else (cs.${builtins.head names}).config.system.nixos.release`).
		ReadOnly().Quiet().Run(x)
	if v := releaseOf(res.Stdout); err == nil && v != "" {
		return v, nil
	}
	if v := inventoryStateVersion(x); v != "" {
		return v, nil
	}
	return "", errors.New("could not tell which NixOS release the inventory installs: give it with --state-version")
}

func writeNewHostFolder(x *engine.Ctx) error {
	address, err := inventoryAddressOf(x)
	if err != nil {
		return err
	}
	secretsURL, err := adoptSecretsAddress(x)
	if err != nil {
		return err
	}
	x.Values["imp_address"] = address
	accounts, err := newHostAccounts(x)
	if err != nil {
		return err
	}
	release, err := newHostRelease(x)
	if err != nil {
		return err
	}
	loader := "systemd-boot"
	if x.Value("boot") == "bios" {
		loader = "grub"
	}
	f := machineFacts{
		Host: x.Value("host"), Platform: x.Value("platform"), Boot: x.Value("boot"), Loader: loader,
		Backend: x.Value("network"), Timezone: x.Value("timezone"), Locale: x.Value("locale"),
		StateVersion: release, Role: x.Value("role"), MachineType: x.Value("machine-type"), Accounts: accounts,
	}
	s := fleetSettings{RepoURL: address, SecretsURL: secretsURL, SignersPath: signersFrom(x.Value("imp_hosts_dir"))}
	doc := newHostDefault(f, newHostDisk{Device: x.Value("disk"), Encrypt: x.Value("encrypt") == "true"}, s)
	rel := x.Value("imp_paths")
	x.UI.Text("default.nix, as fleetmgr writes it:\n\n" + doc)
	if ok, err := x.Approve("Write " + x.Value("flake") + "/" + rel + "/default.nix?"); !ok || err != nil {
		return err
	}
	if err := writeHostFolder(filepath.Join(x.Value("flake"), filepath.FromSlash(rel)), map[string][]byte{"default.nix": []byte(doc)}); err != nil {
		return err
	}
	x.Kept("new folder", rel+" in {{.flake}}, not committed: look at it, change it if it does not evaluate, and run this again to go on from it")
	x.UI.OK("wrote " + rel + "/default.nix")
	return nil
}

func commitNewHost(x *engine.Ctx) error {
	x.Values["imp_subject"] = newHostSubject(x.Value("host"))
	x.Values["imp_body"] = "Written by fleetmgr host new from the answers it was given, before the host is installed. It fetches " + x.Value("imp_address") + "."
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "commit", "-q", "-m", "{{.imp_subject}}", "-m", "{{.imp_body}}", "--", "{{.imp_paths}}").
		OK("committed: {{.imp_subject}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	x.Kept("new folder", "")
	x.Kept("push import", "the commit “{{.imp_subject}}” in {{.flake}}, not pushed yet (git -C {{.flake}} push origin HEAD:refs/heads/main)")
	return nil
}

// takeNewHostEnrollment reads the enrollment file, from --from or from the
// path it waits for the file at, and checks it is this host's.
func takeNewHostEnrollment(x *engine.Ctx) error {
	from := x.Value("from")
	if from == "" {
		from = filepath.Join(x.Value("repo"), x.Value("host")+".enroll")
		if !fileExists(from) {
			if !x.UI.Interactive() || x.Options.Yes {
				return fmt.Errorf("the installer shows %s's enrollment file once it has made the host's identity: copy it here and run this again with --from <file>", x.Value("host"))
			}
			x.Say("On the installer, once it shows {{.host}}'s enrollment file, copy the file to " + from +
				" (it holds public keys only; the secrets checkout's .gitignore keeps *.enroll out), or type it in from its QR code.")
			if err := engine.WaitFor(x, "Is the file at "+from+"?"); err != nil {
				return err
			}
			if !fileExists(from) {
				return fmt.Errorf("there is no file at %s: copy it there, or give --from <file>, and run this again", from)
			}
		}
	}
	doc, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	e, err := secrets.ParseEnrollment(string(doc))
	if err != nil {
		return err
	}
	switch {
	case e.Version != "1":
		return fmt.Errorf("unsupported enrollment file version '%s'", e.Version)
	case e.Host != x.Value("host"):
		return fmt.Errorf("%s is the enrollment file of '%s', not of %s", from, e.Host, x.Value("host"))
	case !strings.HasPrefix(e.AgeRecipient, "age1"):
		return errors.New("malformed age_recipient in the enrollment file")
	case !strings.HasPrefix(e.DeployKey, "ssh-ed25519 "):
		return errors.New("secrets_deploy_pubkey in the enrollment file is not an ssh-ed25519 key")
	}
	address := x.Value("imp_address")
	if address == "" {
		if address, err = inventoryAddressOf(x); err != nil {
			return err
		}
	}
	x.Values["from"], x.Values["recipient"], x.Values["deploy_key"], x.Values["config_key"] = from, e.AgeRecipient, e.DeployKey, e.ConfigKey
	x.Values["config_url"] = address
	if sshURL(address) && e.ConfigKey == "" {
		return errors.New("the enrollment file lists no configuration key, which a host of a private inventory fetches it with: the installer's fleetmgr-secrets-sync predates them")
	}
	x.UI.OK("the enrollment file of " + e.Host + ", recipient " + e.AgeRecipient)
	return nil
}

// newSupportKeysNeeded says whether support's keys are still to be stored.
func newSupportKeysNeeded(x *engine.Ctx) (bool, string) {
	if x.Value("support-keys") != "" {
		return true, ""
	}
	if storedFor(x, x.Value("user"), "authorized_keys") {
		return false, "host/" + x.Value("host") + " holds them already, and --support-keys gives none"
	}
	return true, ""
}

// storedFor says whether the host's branch holds the value at key of the
// user's file on the host: users/<user>/<host>.yaml.
func storedFor(x *engine.Ctx, user, key string) bool {
	ref := secrets.BranchRef(x, x.Value("host"))
	if ref == "" {
		return false
	}
	doc, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "show", ref+":users/"+user+"/"+x.Value("host")+".yaml")
	if err != nil {
		return false
	}
	has, _ := secrets.HasKey(doc, key)
	return has
}

// storeNewHostPasswords stores each account's password, asked for here,
// unless it has one stored already or is given --no-password.
func storeNewHostPasswords(x *engine.Ctx) error {
	accounts, err := newHostAccounts(x)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		switch {
		case !a.Password:
			x.UI.Skip(a.Name + " gets no password: --no-password")
			continue
		case storedFor(x, a.Name, "password"):
			x.UI.OK(a.Name + " has a password stored already")
			continue
		case x.Options.DryRun:
			x.UI.Skip("dry run: would ask for " + a.Name + "'s password twice and store its hash")
			continue
		}
		if err := runSecretsFor(x, SecretsUserPassword, a.Name, map[string]string{}); err != nil {
			return err
		}
	}
	return nil
}

// storeNewHostKeys stores the login keys --keys gives.
func storeNewHostKeys(x *engine.Ctx) error {
	for _, k := range lines(x.Value("keys")) {
		name, file, _ := strings.Cut(k, "=")
		keys, err := keyLinesOf(file)
		if err != nil {
			return err
		}
		if x.Options.DryRun {
			x.UI.Skip(fmt.Sprintf("dry run: would store the %d key(s) in %s for %s", len(keys), file, name))
			continue
		}
		if err := storeKeysFor(x, name, strings.Join(keys, "\n")); err != nil {
			return err
		}
	}
	return nil
}

func showNewHostNext(x *engine.Ctx) error {
	x.Say("{{.host}} is enrolled and its keys are registered. Press Enter on the installer: it clones the inventory with the host's own key, " +
		"fetches its secrets, and installs. After its first boot, check it with:\n  fleetmgr host check {{.host}} --flake " + x.Value("flake"))
	return nil
}
