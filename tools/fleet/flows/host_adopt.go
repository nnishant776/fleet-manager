package flows

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// HostAdopt brings the owner of a personal machine into the fleet: it reads
// the owner from the host, stores their password hash and login keys in the
// secrets, and prints the declaration the maintainer adds to the host's own
// default.nix. It changes nothing in the inventory.
var HostAdopt = &engine.Flow{
	Path:  []string{"host", "adopt"},
	Title: "Adopt a personal machine's owner into the fleet",
	Short: "Store the password and keys of the owner a personal machine made at its first boot, and print the declaration to add",
	Long: "A personal machine (conf.fleet.personal) lets its owner make their own account at the first boot, and\n" +
		"keeps that account out of the configuration. This starts bringing it in. fleet logs in to the host as\n" +
		"support and reads who the owner is (/var/lib/fleet/owner), their full name and home, their password hash\n" +
		"(never shown, and passed on standard input only) and the login keys in their ~/.ssh/authorized_keys.\n" +
		"It stores the hash and the keys in the secrets repository (fleet secrets user-password --hash-stdin and\n" +
		"user-keys), after fleet secrets user-enroll when you give the owner's enrollment file with --from, which\n" +
		"is for an owner who will change their own password and keys later (fleet user init makes the file).\n" +
		"It changes nothing in the inventory. It prints the declaration for you to put into the host's own\n" +
		"default.nix, and the commands that finish: publish with a sync, and check the host. The owner keeps\n" +
		"their uid, files and password: NixOS keeps an existing account's ids when it becomes declared.",
	Example: "  fleet host adopt laptop1 --flake inventory --repo secrets\n" +
		"  fleet host adopt laptop1 --from ann.enroll --flake inventory --repo secrets",
	Args: []string{"host"},
	Inputs: append(append(fleet.HostInputs(), secrets.RepoInput, secrets.RecoveryInput),
		engine.Input{Name: "from", Prompt: "Owner's enrollment file", Optional: true,
			Help: "The owner's enrollment file (fleet user init), when they will manage their own password and keys later; none: they ask you"}),
	Needs: []string{"git", "nix", "tar", "ssh", "yq", "sops", "age", "ssh-keygen"},
	Steps: []engine.Step{
		{
			Title: "Read how {{.host}} is configured",
			Why:   "Only a personal machine has an owner to adopt. Whether the owner is an administrator is read from its configuration, and carried over. Nothing is written.",
			Do:    engine.Func(readAdoptConfig),
		},
		hostReachStep,
		{
			Title: "Read the owner from {{.host}}",
			Why: "As support, with sudo: who made the account (/var/lib/fleet/owner), what getent says of it, the password hash from the shadow file and the login keys in the owner's home. " +
				"The hash is kept in memory only, never shown, and passed on standard input.",
			Do: engine.Func(readOwner),
		},
		secrets.Open(true),
		skipInDryRun(secrets.Unlock()),
		{
			Title: "Enroll the owner in the secrets repository",
			Why: "With their enrollment file the owner gets a branch of their own, user/<owner>, and keeps their password and keys there with fleet user. " +
				"This is fleet secrets user-enroll, run for the owner and this host.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("from") == "" {
					return false, "no enrollment file was given: the password and keys stay on the host's branch, and the owner asks you to change them"
				}
				return true, ""
			},
			Do: engine.Func(enrollOwner),
		},
		{
			Title: "Store the owner's password hash",
			Why:   "As it is on the host: the owner keeps the password they have. This is fleet secrets user-password --hash-stdin.",
			Do:    engine.Func(storeOwnerHash),
		},
		{
			Title: "Store the owner's login keys",
			Why:   "The keys of their ~/.ssh/authorized_keys, which the configuration will read from the secrets instead. This is fleet secrets user-keys --authorized-keys.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("owner_keys") == "" {
					return false, "the owner has no login keys"
				}
				return true, ""
			},
			Do: engine.Func(storeOwnerKeys),
		},
		{
			Title: "Push the secrets",
			Why:   "{{.host}} fetches them from the forge on its next sync, and accepts the commits only if a key it trusts signed them. What the commands above left unpushed goes now.",
			Do:    engine.Func(pushOwnerSecrets),
		},
		{
			Title: "Next",
			Why:   "The declaration to add to the inventory, and the commands that finish.",
			Do:    engine.Func(showAdoptNext),
		},
	},
}

// skipInDryRun leaves a step out of a dry run.
func skipInDryRun(s engine.Step) engine.Step {
	own := s.When
	s.When = func(x *engine.Ctx) (bool, string) {
		if x.Options.DryRun {
			return false, "dry run: nothing is unlocked"
		}
		if own != nil {
			return own(x)
		}
		return true, ""
	}
	return s
}

var (
	// ownerName is what the setup lets an account be called; the secrets
	// repository takes a narrower set (secrets.NamePattern).
	ownerNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	homePattern      = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
	shadowPattern    = regexp.MustCompile(shadowHash)
	namePattern      = regexp.MustCompile(secrets.NamePattern)
)

func readAdoptConfig(x *engine.Ctx) error {
	res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations.{{.host}}.config.conf.fleet.personal").
		ReadOnly().Quiet().Run(x)
	if err != nil {
		return fmt.Errorf("reading %s's configuration: %w: %s", x.Value("host"), err, strings.TrimSpace(res.Stderr))
	}
	var p struct {
		Enable          bool `json:"enable"`
		OwnerPrivileged bool `json:"ownerPrivileged"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &p); err != nil {
		return fmt.Errorf("conf.fleet.personal: %w", err)
	}
	if !p.Enable {
		return fmt.Errorf("%s is not a personal machine: there is nothing to adopt", x.Value("host"))
	}
	x.Values["adopt_privileged"] = fmt.Sprint(p.OwnerPrivileged)
	if p.OwnerPrivileged {
		x.UI.OK(x.Value("host") + " is a personal machine whose owner is an administrator")
	} else {
		x.UI.OK(x.Value("host") + " is a personal machine whose owner is an ordinary user")
	}
	// Where the host's folder is, for the text at the end; config/hosts when
	// the layout cannot be read.
	x.Values["adopt_hosts_dir"] = "config/hosts"
	if res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#fleetLayout").ReadOnly().Quiet().Run(x); err == nil {
		var l layout
		if json.Unmarshal([]byte(res.Stdout), &l) == nil {
			if d, err := cleanLayoutDir("hosts", l.Hosts); err == nil {
				x.Values["adopt_hosts_dir"] = d
			}
		}
	}
	return nil
}

// readOwner reads the owner from the host and keeps what it finds as the
// values owner, owner_full, owner_home, owner_keys and, in memory and never
// shown, owner_hash. The uid is shown for information and kept nowhere.
func readOwner(x *engine.Ctx) error {
	res, err := fleet.OnHost("sudo -n cat /var/lib/fleet/owner").RunQuiet(x)
	if err != nil {
		if res.Code == 255 {
			return err
		}
		return fmt.Errorf("nobody has set %s up yet: /var/lib/fleet/owner is not there, or support cannot read it (%v)", x.Value("host"), err)
	}
	owner := strings.TrimSpace(res.Stdout)
	if owner == "" {
		return fmt.Errorf("nobody has set %s up yet: /var/lib/fleet/owner is empty", x.Value("host"))
	}
	if !ownerNamePattern.MatchString(owner) {
		return fmt.Errorf("/var/lib/fleet/owner holds %q, which is not a user name", owner)
	}
	if !namePattern.MatchString(owner) {
		return fmt.Errorf("%s cannot be a user in the secrets repository, whose names are lowercase letters, digits and '-', and start with a letter or digit", owner)
	}
	x.Values["owner"] = owner

	res, err = fleet.OnHost("getent passwd " + owner).RunQuiet(x)
	if err != nil {
		return fmt.Errorf("%s has no account %s: getent passwd finds nothing", x.Value("host"), owner)
	}
	f := strings.Split(strings.TrimSpace(res.Stdout), ":")
	if len(f) < 7 || f[0] != owner {
		return fmt.Errorf("unexpected answer of getent passwd %s: %q", owner, strings.TrimSpace(res.Stdout))
	}
	uid := f[2]
	if _, err := strconv.Atoi(uid); err != nil {
		return fmt.Errorf("the uid %q of %s is not a number", uid, owner)
	}
	home := f[5]
	if !homePattern.MatchString(home) {
		return fmt.Errorf("the home %q of %s is not a plain path", home, owner)
	}
	full, _, _ := strings.Cut(f[4], ",")
	x.Values["owner_full"], x.Values["owner_home"] = full, home

	// The hash stays in memory: it is captured as a secret, which is never
	// shown, and taken out of the line it comes in.
	if _, err := fleet.OnHost("sudo -n getent shadow " + owner).CaptureSecret("owner_shadow").ReadOnly().Quiet().Run(x); err != nil {
		return fmt.Errorf("reading %s's password hash on the host as support (sudo getent shadow): %w", owner, err)
	}
	line := x.Value("owner_shadow")
	delete(x.Values, "owner_shadow")
	sf := strings.Split(line, ":")
	if len(sf) < 2 || sf[0] != owner {
		return fmt.Errorf("unexpected answer of getent shadow for %s", owner)
	}
	hash := sf[1]
	if hash == "" || strings.HasPrefix(hash, "!") || strings.HasPrefix(hash, "*") {
		return fmt.Errorf("%s has no password; have them set one with passwd first", owner)
	}
	if !shadowPattern.MatchString(hash) {
		return fmt.Errorf("%s's password hash is not one that can be stored (it is not of the form $id$…)", owner)
	}
	x.Values["owner_hash"] = hash

	script := fmt.Sprintf("if sudo -n test -f '%[1]s/.ssh/authorized_keys'; then sudo -n cat '%[1]s/.ssh/authorized_keys'; fi", home)
	res, err = fleet.OnHost(script).RunQuiet(x)
	if err != nil {
		return fmt.Errorf("reading %s/.ssh/authorized_keys: %w", home, err)
	}
	keys, _ := keyLines(res.Stdout)
	x.Values["owner_keys"] = strings.Join(keys, "\n")

	x.UI.OK(fmt.Sprintf("owner %s, uid %s (it stays as it is)", owner, uid))
	x.UI.OK(fmt.Sprintf("full name %q, home %s", full, home))
	x.UI.OK("a password hash (not shown)")
	x.UI.OK(fmt.Sprintf("%d login key(s) in %s/.ssh/authorized_keys", len(keys), home))
	return nil
}

// secretsBody is the steps of a secrets command without the ones adopt has
// done itself: bringing a named clone up to date, the signers comparison,
// opening the repository and unlocking the identity.
func secretsBody(f *engine.Flow) []engine.Step {
	skip := map[string]bool{
		cloneSync.Title: true, signersCheck.Title: true,
		secrets.Open(true).Title: true, secrets.Unlock().Title: true,
	}
	var steps []engine.Step
	for _, s := range f.Steps {
		if !skip[s.Title] {
			steps = append(steps, s)
		}
	}
	return steps
}

// runSecrets runs a secrets command's steps for the owner on this host. The
// identity is unlocked already, so the command is told it has none to unlock.
func runSecrets(x *engine.Ctx, f *engine.Flow, flags map[string]string) error {
	flags["repo"], flags["recovery"] = x.Value("repo"), ""
	flags["user"], flags["host"] = x.Value("owner"), x.Value("host")
	return x.RunSteps(f.Inputs, flags, secretsBody(f))
}

func keptSecrets(x *engine.Ctx) {
	x.Kept("adopt secrets", "{{.owner}}'s password hash and keys are stored in the secrets repository {{.repo}} and stay there; run this again to go on")
}

func enrollOwner(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: would run the steps of fleet secrets user-enroll " + x.Value("owner") + " --from " + x.Value("from") + " --host " + x.Value("host"))
		return nil
	}
	if err := runSecrets(x, SecretsUserEnroll, map[string]string{"from": x.Value("from")}); err != nil {
		return err
	}
	keptSecrets(x)
	return nil
}

func storeOwnerHash(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: would run the steps of fleet secrets user-password " + x.Value("owner") + " " + x.Value("host") + " --hash-stdin")
		return nil
	}
	if err := runSecrets(x, SecretsUserPassword, map[string]string{"hash": x.Value("owner_hash")}); err != nil {
		return err
	}
	keptSecrets(x)
	return nil
}

func storeOwnerKeys(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: would run the steps of fleet secrets user-keys " + x.Value("owner") + " " + x.Value("host") + " --authorized-keys <their keys>")
		return nil
	}
	dir, err := os.MkdirTemp("", "fleet-adopt-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(file, []byte(x.Value("owner_keys")+"\n"), 0o600); err != nil {
		return err
	}
	if err := runSecrets(x, SecretsUserKeys, map[string]string{"authorized-keys": file}); err != nil {
		return err
	}
	keptSecrets(x)
	return nil
}

// pushOwnerSecrets pushes the branch the commands above worked on, unless
// they pushed it.
func pushOwnerSecrets(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: the branch holding the owner's files would be pushed")
		return nil
	}
	branch := secrets.WorkBranch(x)
	n, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-list", "--count", "refs/remotes/origin/"+branch+"..refs/heads/"+branch)
	if err == nil && n == "0" {
		x.UI.OK(branch + " is pushed already")
		return nil
	}
	_, err = secrets.PushWork().OK("pushed " + branch).Run(x)
	return err
}

// nixString is s as a Nix string literal.
func nixString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '$' && i+1 < len(s) && s[i+1] == '{':
			b.WriteString(`\$`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// nixAttr is a name as an attribute name: bare when it can be, else quoted.
func nixAttr(name string) string {
	if regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`).MatchString(name) {
		return name
	}
	return nixString(name)
}

// adoptDeclaration is the account's declaration, to put into the host's
// default.nix.
func adoptDeclaration(name, full string, privileged, keys bool) string {
	var b strings.Builder
	b.WriteString("conf.users.accounts." + nixAttr(name) + " = {\n")
	b.WriteString("  fullName = " + nixString(full) + ";\n")
	b.WriteString("  privileged = " + strconv.FormatBool(privileged) + ";\n")
	b.WriteString("  secrets.password = true;\n")
	if keys {
		b.WriteString("  secrets.authorizedKeys = true;\n")
	}
	b.WriteString("};")
	return b.String()
}

func showAdoptNext(x *engine.Ctx) error {
	x.Kept("adopt secrets", "")
	host, owner, flake := x.Value("host"), x.Value("owner"), x.Value("flake_given")
	if flake == "" {
		flake = x.Value("flake")
	}
	keys := x.Value("owner_keys") != ""
	enrolled := x.Value("from") != ""
	stored := owner + "'s password hash"
	switch {
	case keys && enrolled:
		stored += ", keys and enrollment are"
	case keys:
		stored += " and keys are"
	case enrolled:
		stored += " and enrollment are"
	default:
		stored += " is"
	}
	dir := x.Value("adopt_hosts_dir")
	if dir == "" {
		dir = "config/hosts"
	}
	var b strings.Builder
	b.WriteString(stored + " in the secrets. Now declare the account in the inventory and take the host out of personal mode, in " +
		dir + "/" + host + "/default.nix (the layout's hosts dir):\n\n")
	decl := adoptDeclaration(owner, x.Value("owner_full"), x.Value("adopt_privileged") == "true", keys)
	for _, l := range strings.Split(decl, "\n") {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\nand remove conf.fleet.personal.enable = true (or set it to false).\n" +
		"Then commit, push, and publish with a sync:\n" +
		"  fleet inventory publish " + host + " --flake " + flake + " --sync\n" +
		"  fleet host check " + host + " --flake " + flake + "\n\n" +
		owner + " keeps their uid, files and password. From then on ")
	if enrolled {
		b.WriteString("they change their password and keys with fleet user password / fleet user keys")
	} else {
		b.WriteString("their password and keys change through you, fleet secrets user-password / user-keys")
	}
	b.WriteString("; passwd on the machine no longer sticks, and ~/.ssh/authorized_keys no longer admits them.")
	x.UI.Text(b.String())
	return nil
}
