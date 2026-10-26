package flows

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// The commands of a user who has a branch of their own, user/<user> (see
// internal/secrets/users.go), change their files there themselves: their
// password, their login and sudo keys, and secrets of their own that their
// hosts hand to them. Each works in a temporary worktree of the branch of the
// user's clone of the secrets repository, decrypts with the user's own
// identity, signs the commit with the user's own key and pushes the branch.
// The maintainer's tools are the ones that create the files.

// secretNamePattern is what the name of a secret of one's own may be: it is
// a key under secrets in the user's file, and a name for fleet-user-secret.
const secretNamePattern = `^[A-Za-z0-9_-]+$`

// userEdit is one change of a user's files, made on each host chosen.
type userEdit struct {
	inputs []engine.Input // flags of its own
	pre    []engine.Step  // checks and preparation, before anything is fetched
	title  string         // of the step that changes the files
	why    string
	// edit changes one file of the worktree, users/<user>/<host>.yaml, and
	// says whether it did; a change that changes nothing makes no commit.
	edit func(x *engine.Ctx, file string) (bool, error)
	// message is the commit message, given the hosts that changed.
	message func(x *engine.Ctx, hosts string) string
}

// hostsInput is the hosts to change, given as arguments or as --host.
var hostsInput = engine.Input{
	Name: "host", Prompt: "Hosts to change", Optional: true, Many: true, Pattern: secrets.NamePattern,
	Help:    "A host whose file of yours is changed; may be given more than once. Or use --all-hosts",
	Default: func(*engine.Ctx) (string, error) { return "", nil },
}

var allHostsInput = engine.Input{
	Name: "all-hosts", Prompt: "Change your file on every host?", Bool: true,
	Help:    "Change your file on every host that has one on your branch, instead of the hosts named",
	Default: func(*engine.Ctx) (string, error) { return "false", nil },
}

// ownFlakeInput is where the configuration repository's keys are found: the
// checkout whose origin names the copy of its signers you trust, if any.
func ownFlakeInput() engine.Input {
	in := fleet.FlakeInput
	in.Optional = true
	in.Help = "A checkout of the configuration repository, whose origin names your copy of its signers (fleet trust signers); defaults to this directory's flake, then /etc/nixos. Not needed with --signers"
	return in
}

var signersInput = engine.Input{
	Name: "signers", Prompt: "Signers file", Optional: true,
	Help:    "An allowed_signers file to check your branch's newest commit against, besides your own key; by default your copy of the configuration repository's",
	Default: func(*engine.Ctx) (string, error) { return "", nil },
}

// userEditFlow makes the flow of a command that changes a user's files on
// their branch: the shared inputs and steps around e.
func userEditFlow(f engine.Flow, e userEdit) *engine.Flow {
	f.Rest = true
	f.Args = append(f.Args, "host")
	f.Inputs = append(append([]engine.Input{secrets.RepoInput, ownUserName()}, e.inputs...),
		hostsInput, allHostsInput, identityInput, signingKeyInput, enrollInput, ownFlakeInput(), signersInput)
	f.Needs = append([]string{"git", "sops", "age", "ssh-keygen"}, f.Needs...)
	steps := slices.Clone(e.pre)
	steps = append(steps,
		engine.Step{
			Title: "Open the secrets repository",
			Why:   "Your clone of it; your branch, user/{{.user}}, is changed in a temporary worktree of it, so that your checkout stays as it is.",
			Do: engine.Checks(engine.Probe{
				What:   "{{.repo}} is a git repository",
				Cmd:    repoGit("rev-parse", "--git-dir"),
				Expect: func(string) (string, error) { return "", nil },
			}),
		},
		engine.Step{
			Title: "Fetch user/{{.user}} from origin",
			Why:   "To build on the newest commit of your branch, which the maintainer or you may have added to since.",
			Do: repoGit("fetch", "-q", "origin", "+refs/heads/user/{{.user}}:refs/remotes/origin/user/{{.user}}").ReadOnly().
				Explain(func(engine.Result) string {
					return "could not fetch user/{{.user}} from origin: is there such a branch? The maintainer makes it with fleet secrets user-enroll {{.user}} --from {{.user}}.enroll"
				}).OK("fetched"),
		},
		engine.Step{
			Title: "Find your branch",
			Why:   "Your clone's user/{{.user}}, or else origin's. A local branch that origin's has moved on from is brought up to it; one with commits origin lacks is used as it is.",
			Do:    engine.Func(findOwnBranch),
		},
		engine.Step{
			Title: "Check who signed its newest commit",
			Why: "You build on it, so it must be signed by your own key or by a key in your copy of the configuration repository's signers (the maintainers'): " +
				"a pushed commit by anyone else is not to be built on.",
			Do: engine.Func(verifyOwnBranch),
		},
		engine.Step{
			Title: "Choose the hosts",
			Why:   "The hosts named, each of which must have a file of yours on your branch (the maintainer's fleet secrets user-enroll makes them), or all of them with --all-hosts.",
			Do:    engine.Func(chooseOwnHosts),
		},
		engine.Step{
			Title: "Unlock your identity",
			Why:   "Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("identity") == "" {
					return false, "no identity given; sops reads SOPS_AGE_KEY_FILE or SOPS_AGE_KEY"
				}
				return true, ""
			},
			Do: engine.Cmd("age", "-d", "{{.identity}}").ReadOnly().Prompts().CaptureEnv("SOPS_AGE_KEY", "sops").OK("unlocked for this run"),
		},
		secrets.OpenWork(),
		engine.Step{Title: e.title, Why: e.why, Do: engine.Func(func(x *engine.Ctx) error { return editOwnFiles(x, e) })},
		engine.Step{
			Title: "Commit, signed with your key",
			Why: "Signed with your own signing key, which must be in your agent, or ssh-keygen asks for its passphrase; hosts accept your commit only if it is signed by it. " +
				"Your git configuration is not touched.",
			When: changedHosts,
			Do:   engine.Func(func(x *engine.Ctx) error { return commitOwn(x, e) }),
		},
		secrets.CloseWork(),
		engine.Step{
			Title: "Push {{.branch}}",
			Why:   "Your hosts fetch your branch on their next sync, which checks the commit is signed by you, and that it is encrypted to the right recipients.",
			When:  changedHosts,
			Do:    secrets.PushWork(),
		},
	)
	f.Steps = steps
	return &f
}

func changedHosts(x *engine.Ctx) (bool, string) {
	if x.Value("changed") == "" {
		return false, "nothing changed"
	}
	return true, ""
}

// findOwnBranch keeps the ref of the user's branch as ref, and branch.
func findOwnBranch(x *engine.Ctx) error {
	user := x.Value("user")
	local, remote := "refs/heads/user/"+user, "refs/remotes/origin/user/"+user
	has := func(ref string) bool {
		_, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", "--verify", "-q", ref)
		return err == nil
	}
	haveLocal, haveRemote := has(local), has(remote)
	switch {
	case !haveLocal && !haveRemote:
		return fmt.Errorf("there is no user/%s in %s, local or on origin: the maintainer makes it with fleet secrets user-enroll %s --from %s.enroll", user, x.Value("repo"), user, user)
	case haveLocal && haveRemote:
		l, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", local)
		r, _ := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "rev-parse", remote)
		if l != r {
			if _, err := engine.Silently(x, nil, "git", "-C", x.Value("repo"), "merge-base", "--is-ancestor", local, remote); err == nil {
				if _, err := repoGit("branch", "-f", "-q", "user/"+user, remote).OK("brought your user/" + user + " up to origin's").Run(x); err != nil {
					return err
				}
			} else {
				x.UI.Note("Your user/" + user + " has commits origin's lacks (or the two have diverged); it is used as it is, and the push may be refused.")
			}
		}
	case haveRemote:
		local = remote
	}
	x.Values["ref"], x.Values["branch"] = local, "user/"+user
	x.UI.OK(local)
	return nil
}

// signingKeyOf is the user's signing key, type and material, from their
// enrollment file, or else from the public half of their signing key.
func signingKeyOf(x *engine.Ctx) (string, error) {
	if b, err := os.ReadFile(x.Value("enroll")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "signing_key:"); ok {
				if f := strings.Fields(strings.Trim(strings.TrimSpace(v), `"'`)); len(f) >= 2 {
					return f[0] + " " + f[1], nil
				}
			}
		}
		return "", fmt.Errorf("%s holds no signing_key", x.Value("enroll"))
	}
	if b, err := os.ReadFile(x.Value("signing-key") + ".pub"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 2 {
			return f[0] + " " + f[1], nil
		}
	}
	return "", fmt.Errorf("no enrollment file %s and no %s.pub: run fleet user init first", x.Value("enroll"), x.Value("signing-key"))
}

// trustedSignersData is the content of the allowed_signers file the branch's
// newest commit may be signed by besides the user's own key: --signers, else
// the copy of the configuration repository's, if the user has made one.
func trustedSignersData(x *engine.Ctx) ([]byte, string, error) {
	if p := x.Value("signers"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("--signers: %w", err)
		}
		return b, p, nil
	}
	flake := x.Value("flake")
	if flake == "" {
		return nil, "", nil
	}
	origin, err := engine.Silently(x, nil, "git", "-C", flake, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return nil, "", nil
	}
	path, _, err := fleet.TrustedSigners(origin)
	switch {
	case errors.Is(err, fleet.ErrNoSignersCopy):
		return nil, "", nil
	case err != nil:
		return nil, "", err
	}
	b, err := os.ReadFile(path)
	return b, path, err
}

func verifyOwnBranch(x *engine.Ctx) error {
	key, err := signingKeyOf(x)
	if err != nil {
		return err
	}
	signers, from, err := trustedSignersData(x)
	if err != nil {
		return err
	}
	if from == "" {
		x.UI.Note("You have no copy of the configuration repository's signers (fleet trust signers --flake <checkout>), and gave no --signers: only your own key counts.")
	}
	if err := secrets.VerifyOwnTip(x, x.Value("ref"), x.Value("user"), key, signers); err != nil {
		return err
	}
	if from == "" {
		x.UI.OK("its newest commit is signed by your own key")
	} else {
		x.UI.OK("its newest commit is signed by your own key or by a key in " + from)
	}
	return nil
}

// chooseOwnHosts keeps the hosts to change as hosts, one per line.
func chooseOwnHosts(x *engine.Ctx) error {
	user, ref := x.Value("user"), x.Value("ref")
	given := strings.Fields(x.Value("host"))
	all := x.Value("all-hosts") == "true"
	switch {
	case all && len(given) > 0:
		return errors.New("give the hosts, or --all-hosts, not both")
	case !all && len(given) == 0:
		return errors.New("give the hosts to change, or --all-hosts")
	}
	var hosts []string
	if all {
		res, err := repoGit("ls-tree", "--name-only", ref, "users/"+user+"/").RunQuiet(x)
		if err != nil {
			return err
		}
		for _, p := range strings.Fields(res.Stdout) {
			if h, ok := strings.CutSuffix(strings.TrimPrefix(p, "users/"+user+"/"), ".yaml"); ok && h != "" {
				hosts = append(hosts, h)
			}
		}
		if len(hosts) == 0 {
			return fmt.Errorf("%s holds no file of yours", ref)
		}
	} else {
		for _, h := range given {
			if !hasPath(x, ref, userFilePath(user, h)) {
				return fmt.Errorf("%s has no %s: the maintainer lists you on %s and makes it with fleet secrets user-enroll %s --from %s.enroll --host %s", ref, userFilePath(user, h), h, user, user, h)
			}
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	slices.Sort(hosts)
	x.Values["hosts"] = strings.Join(hosts, "\n")
	x.UI.OK(strings.Join(hosts, ", "))
	return nil
}

// editOwnFiles runs the change on the file of each host chosen and keeps the
// hosts it changed as changed, one per line.
func editOwnFiles(x *engine.Ctx, e userEdit) error {
	var changed []string
	for _, h := range strings.Fields(x.Value("hosts")) {
		x.UI.Note(h + ":")
		did, err := e.edit(x, userFilePath(x.Value("user"), h))
		if err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
		if did {
			changed = append(changed, h)
		}
	}
	x.Values["changed"] = strings.Join(changed, "\n")
	if len(changed) == 0 {
		x.UI.OK("nothing changed: no commit is made")
	}
	return nil
}

// commitOwn commits the files of the hosts that changed, signed with the
// user's own key and with nothing taken from their git configuration but the
// name and address of the commit.
func commitOwn(x *engine.Ctx, e userEdit) error {
	hosts := strings.Fields(x.Value("changed"))
	args := []string{"add"}
	for _, h := range hosts {
		args = append(args, userFilePath(x.Value("user"), h))
	}
	if _, err := engine.Cmd("git", args...).In("{{.work}}").Run(x); err != nil {
		return err
	}
	msg := e.message(x, strings.Join(hosts, ", "))
	branch := secrets.WorkBranch(x)
	_, err := engine.Cmd("git", "-c", "gpg.format=ssh", "-c", "user.signingKey={{.signing-key}}.pub", "-c", "commit.gpgSign=true",
		"commit", "-q", "-m", msg).In("{{.work}}").OK("committed: " + msg).After(func(x *engine.Ctx) error {
		x.Settled()
		x.Kept("push "+branch, "the commit “"+msg+"” on "+branch+" in {{.repo}}, not pushed yet (git -C {{.repo}} push origin "+branch+")")
		return nil
	}).Run(x)
	return err
}

// UserPassword sets the user's password on their hosts.
var UserPassword = userEditFlow(engine.Flow{
	Path:  []string{"user", "password"},
	Title: "Set your password on your hosts",
	Short: "Set your password on hosts, on your own branch",
	Long: "Hashes the password (yescrypt) and stores the hash in your file for each host, users/<user>/<host>.yaml,\n" +
		"on your branch, user/<user>, of your clone of the secrets repository; commits, signed with your own signing key,\n" +
		"and pushes the branch. Hosts apply the password at their next sync. The branch's newest commit must be signed\n" +
		"by your key or by a key in your copy of the configuration repository's signers before the change builds on it.",
	Example: "  fleet user password laptop1 nas\n  fleet user password --all-hosts\n  printf %s \"$pw\" | fleet user password laptop1 --password-stdin --yes",
	Needs:   []string{"mkpasswd"},
}, userEdit{
	inputs: []engine.Input{{Name: "password", Prompt: "Password for {{.user}}", Help: "Hidden as you type", Secret: true, Twice: true}},
	pre: []engine.Step{{
		Title: "Hash the password",
		Why:   "Only the yescrypt hash is stored; the password itself goes nowhere.",
		Do: engine.Cmd("mkpasswd", "-m", "yescrypt", "--stdin").ReadOnly().
			StdinFrom("password").StdinNote("the password").CaptureSecret("hash").OK("hashed"),
	}},
	title: "Store the hash in your files",
	why:   "As the key password, which hosts read when accounts.<user>.secrets.password is on.",
	edit: func(x *engine.Ctx, file string) (bool, error) {
		x.Values["hash_json"] = secrets.JSON(x.Value("hash"))
		_, err := secrets.Set(file, "password", "hash_json", "the password hash").Run(x)
		return err == nil, err
	},
	message: func(x *engine.Ctx, hosts string) string { return "user-password: " + x.Value("user") + " on " + hosts },
})

// UserKeys sets the user's own login and sudo keys on their hosts.
var UserKeys = userEditFlow(engine.Flow{
	Path:  []string{"user", "keys"},
	Title: "Set your login and sudo keys on your hosts",
	Short: "Set the SSH keys that may log in as you, or unlock your sudo, on hosts, on your own branch",
	Long: "Stores public keys, one per line, as authorized_keys and sudo_keys in your file for each host,\n" +
		"users/<user>/<host>.yaml, on your branch, user/<user>, of your clone of the secrets repository; commits, signed\n" +
		"with your own signing key, and pushes the branch. Each line is checked with ssh-keygen first, so that a typo\n" +
		"cannot lock you out. Hosts read them when accounts.<user>.secrets.authorizedKeys or secrets.sudoKeys is on,\n" +
		"at each login, with no rebuild. --authorized-keys and --sudo-keys replace the keys stored, so list every key\n" +
		"that should stay. --add-… and --remove-… change them instead: they read the stored keys, add the keys not there\n" +
		"yet or remove the ones given (keys are matched by type and key material, so options such as from= and the comment\n" +
		"do not matter), and keep the other lines as they are. A removal that would leave no key is refused; a change that\n" +
		"changes nothing makes no commit.",
	Example: "  fleet user keys laptop1 --authorized-keys my.keys\n" +
		"  fleet user keys --all-hosts --add-authorized-keys new.pub\n" +
		"  fleet user keys laptop1 nas --remove-authorized-keys old.pub --add-sudo-keys sudo.pub",
}, userEdit{
	inputs: keyInputs(),
	pre: []engine.Step{{
		Title: "Check the keys",
		Why:   "A line that is not a public key would be stored as it is and lock you out; each non-empty line must be one for ssh-keygen.",
		Do:    engine.Func(checkKeyFiles),
	}},
	title: "Store the keys in your files",
	why:   "Replaced, or added and removed against the keys stored now, which are read with your identity; a list that would end up empty is refused, and one that does not change is left alone.",
	edit: func(x *engine.Ctx, file string) (bool, error) {
		read := func(key string) (string, error) {
			return storedValue(x, file, `["`+key+`"]`, "the "+key+" keys")
		}
		if err := mergeKinds(x, read); err != nil {
			return false, err
		}
		changed := false
		for _, k := range keyKinds {
			json := x.Values[k.json]
			if json == "" {
				continue
			}
			if x.Value(k.replace) != "" {
				current, err := read(k.key)
				if err != nil {
					return false, err
				}
				if current != "" && secrets.JSON(current) == json {
					x.UI.Skip("the " + k.key + " keys stay as they are: the same keys")
					continue
				}
			}
			if _, err := secrets.Set(file, k.key, k.json, "the "+k.key+" keys").Run(x); err != nil {
				return false, err
			}
			changed = true
		}
		return changed, nil
	},
	message: func(x *engine.Ctx, hosts string) string { return "user-keys: " + x.Value("user") + " on " + hosts },
})

var secretNameInput = engine.Input{
	Name: "name", Prompt: "Name of the secret", Pattern: secretNamePattern,
	Help: "Its key under secrets in your file, and the name fleet-user-secret takes on the host: letters, digits, - and _",
}

// UserSecretSet stores a secret of the user's own on their hosts.
var UserSecretSet = userEditFlow(engine.Flow{
	Path:  []string{"user", "secret", "set"},
	Title: "Store a secret of your own for your hosts",
	Short: "Store a secret of your own, which your hosts hand only to you, on your own branch",
	Long: "Stores the value as secrets.<name> in your file for each host, users/<user>/<host>.yaml, on your branch,\n" +
		"user/<user>, of your clone of the secrets repository; commits, signed with your own signing key, and pushes\n" +
		"the branch. The value is typed hidden or read from standard input. A host whose configuration sets\n" +
		"accounts.<user>.secrets.own decrypts your whole file for you alone, and fleet-user-secret <name> prints the value\n" +
		"there. A value that is stored already makes no commit.",
	Example: "  fleet user secret set token laptop1\n  printf %s \"$token\" | fleet user secret set token --all-hosts --value-stdin --yes",
	Args:    []string{"name"},
}, userEdit{
	inputs: []engine.Input{secretNameInput, {Name: "value", Prompt: "Value of {{.name}}", Help: "Hidden as you type; trailing newlines are dropped", Secret: true}},
	title:  "Store the value in your files",
	why:    "As the key secrets.<name>. A value that is the same as the one stored is left alone.",
	edit: func(x *engine.Ctx, file string) (bool, error) {
		if x.Value("value") == "" && !x.Options.DryRun {
			return false, errors.New("the value is empty")
		}
		path := `["secrets"]["` + x.Value("name") + `"]`
		current, err := storedValue(x, file, path, "secrets."+x.Value("name"))
		if err != nil {
			return false, err
		}
		if current != "" && strings.TrimRight(current, "\n") == strings.TrimRight(x.Value("value"), "\n") {
			x.UI.Skip("secrets." + x.Value("name") + " holds this value already")
			return false, nil
		}
		x.Values["value_json"] = secrets.JSON(x.Value("value"))
		_, err = secrets.SetAt(file, path, "value_json", "the value").Run(x)
		return err == nil, err
	},
	message: func(x *engine.Ctx, hosts string) string {
		return "user-secret: set " + x.Value("name") + " for " + x.Value("user") + " on " + hosts
	},
})

// UserSecretUnset removes a secret of the user's own from their hosts.
var UserSecretUnset = userEditFlow(engine.Flow{
	Path:  []string{"user", "secret", "unset"},
	Title: "Remove a secret of your own from your hosts",
	Short: "Remove a secret of your own from your files, on your own branch",
	Long: "Removes secrets.<name> from your file for each host, users/<user>/<host>.yaml, on your branch, user/<user>,\n" +
		"of your clone of the secrets repository; commits, signed with your own signing key, and pushes the branch.\n" +
		"A host that does not have it makes no commit.",
	Example: "  fleet user secret unset token laptop1\n  fleet user secret unset token --all-hosts",
	Args:    []string{"name"},
}, userEdit{
	inputs: []engine.Input{secretNameInput},
	title:  "Remove the secret from your files",
	why:    "The key secrets.<name>. The file is read with your identity first: one that does not hold it is left alone.",
	edit: func(x *engine.Ctx, file string) (bool, error) {
		path := `["secrets"]["` + x.Value("name") + `"]`
		current, err := storedValue(x, file, path, "secrets."+x.Value("name"))
		if err != nil {
			return false, err
		}
		if current == "" && !x.Options.DryRun {
			x.UI.Skip("secrets." + x.Value("name") + " is not there")
			return false, nil
		}
		_, err = engine.Cmd("sops", "unset", file, path).In("{{.work}}").Run(x)
		return err == nil, err
	},
	message: func(x *engine.Ctx, hosts string) string {
		return "user-secret: unset " + x.Value("name") + " for " + x.Value("user") + " on " + hosts
	},
})
