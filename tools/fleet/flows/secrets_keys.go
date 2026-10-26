package flows

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// A kind of key list in a user's file: the replacing flag, the two that change
// it, the key it is stored under, and the values the steps pass on.
type keyKind struct {
	replace, add, remove string // flag names
	key                  string // key in users/<user>/<host>.yaml
	json                 string // the new content, as a JSON string for sops set
}

var keyKinds = []keyKind{
	{"authorized-keys", "add-authorized-keys", "remove-authorized-keys", "authorized_keys", "authorized_json"},
	{"sudo-keys", "add-sudo-keys", "remove-sudo-keys", "sudo_keys", "sudo_json"},
}

func keyInput(name, prompt, help string) engine.Input {
	return engine.Input{Name: name, Prompt: prompt, Help: help, Optional: true,
		Default: func(*engine.Ctx) (string, error) { return "", nil }}
}

// SecretsUserKeys sets a user's login and sudo keys on a host.
var SecretsUserKeys = &engine.Flow{
	Path:  []string{"secrets", "user-keys"},
	Title: "Set a user's login and sudo keys on a host",
	Short: "Set the SSH keys that may log in as the user, or unlock their sudo, on that host",
	Long: "Stores public keys, one per line, as authorized_keys and sudo_keys in\n" +
		"users/<user>/<host>.yaml on the host's branch, host/<host>, encrypted to that host and your\n" +
		"recovery recipient (the file is created if it is not there yet), then commits and pushes the branch.\n" +
		"Each line is checked with ssh-keygen first, so that a typo cannot lock anyone out. Hosts read them\n" +
		"when accounts.<user>.secrets.authorizedKeys or secrets.sudoKeys is on, at each login, with no\n" +
		"rebuild. --authorized-keys and --sudo-keys replace the keys stored, so list every key that should stay.\n" +
		"--add-… and --remove-… change them instead: they read the stored keys, add the keys not there yet or\n" +
		"remove the ones given (keys are matched by type and key material, so options such as from= and the\n" +
		"comment do not matter), and keep the other lines as they are. A removal that would leave no key is\n" +
		"refused; a change that changes nothing makes no commit.",
	Example: "  fleet secrets user-keys alice laptop1 --authorized-keys alice.keys\n" +
		"  fleet secrets user-keys admin laptop1 --authorized-keys admin.keys --sudo-keys admin-sudo.keys\n" +
		"  fleet secrets user-keys support laptop1 --add-authorized-keys new.pub\n" +
		"  fleet secrets user-keys support laptop1 --remove-authorized-keys old.pub",
	Args: []string{"user", "host"},
	Inputs: []engine.Input{secrets.RepoInput, secrets.RecoveryInput, secrets.UserName(), knownHost(),
		keyInput("authorized-keys", "File of login keys", "SSH public keys that may log in as the user, one per line, replacing those stored; empty to leave them as they are"),
		keyInput("add-authorized-keys", "File of login keys to add", "SSH public keys to add to those stored; empty to add none"),
		keyInput("remove-authorized-keys", "File of login keys to remove", "SSH public keys to remove from those stored, matched by type and key material; empty to remove none"),
		keyInput("sudo-keys", "File of sudo keys", "SSH public keys that unlock the user's sudo, one per line, replacing those stored; empty to leave them as they are"),
		keyInput("add-sudo-keys", "File of sudo keys to add", "SSH public keys to add to the sudo keys stored; empty to add none"),
		keyInput("remove-sudo-keys", "File of sudo keys to remove", "SSH public keys to remove from the sudo keys stored, matched by type and key material; empty to remove none"),
	},
	Needs: []string{"git", "yq", "sops", "age", "ssh-keygen"},
	Steps: []engine.Step{
		{
			Title: "Check the keys",
			Why:   "A line that is not a public key would be stored as it is and lock out whoever relies on it; each non-empty line must be one for ssh-keygen.",
			Do:    engine.Func(checkKeyFiles),
		},
		secrets.Open(true),
		secrets.ExistingBranch("host '{{.host}}' is not enrolled; use host-enroll first", true),
		secrets.Unlock(),
		secrets.OpenWork(),
		createUserFile(),
		{
			Title: "Work out the keys to store",
			Why:   "Added and removed keys are applied to the keys stored now, which are read with your recovery identity; a key list that would end up empty is refused, and one that does not change is left alone.",
			When: func(x *engine.Ctx) (bool, string) {
				if mergeRequested(x) {
					return true, ""
				}
				return false, "the keys are replaced, not changed"
			},
			Do: engine.Func(mergeKeyFiles),
		},
		{
			Title: "Store the login keys in " + userFile(),
			Why:   "As the key authorized_keys, which hosts read when accounts.<user>.secrets.authorizedKeys is on.",
			When:  keysToStore(keyKinds[0]),
			Do:    secrets.Set(userFile(), "authorized_keys", "authorized_json", "the login keys"),
		},
		{
			Title: "Store the sudo keys in " + userFile(),
			Why:   "As the key sudo_keys, which hosts read when accounts.<user>.secrets.sudoKeys is on.",
			When:  keysToStore(keyKinds[1]),
			Do:    secrets.Set(userFile(), "sudo_keys", "sudo_json", "the sudo keys"),
		},
		withChange(secrets.Commit("user-keys: {{.user}} on {{.host}}", userFile())),
		secrets.CloseWork(),
		withChange(engine.Step{
			Title: "Push host/{{.host}}",
			Why:   "{{.host}} fetches its branch from the forge; it accepts the commit only if it is signed by an allowed key.",
			Do:    secrets.PushBranch(),
		}),
	},
}

func mergeRequested(x *engine.Ctx) bool {
	for _, k := range keyKinds {
		if x.Value(k.add) != "" || x.Value(k.remove) != "" {
			return true
		}
	}
	return false
}

func keysToStore(k keyKind) func(*engine.Ctx) (bool, string) {
	return func(x *engine.Ctx) (bool, string) {
		if x.Value(k.json) == "" {
			return false, "no change to the " + k.key + " keys"
		}
		return true, ""
	}
}

// withChange runs a step only when some key list is to be stored.
func withChange(s engine.Step) engine.Step {
	s.When = func(x *engine.Ctx) (bool, string) {
		for _, k := range keyKinds {
			if x.Value(k.json) != "" {
				return true, ""
			}
		}
		return false, "nothing changed"
	}
	return s
}

// keyLines are the lines of a key file that are neither empty nor comments,
// with their line numbers.
func keyLines(text string) (lines []string, numbers []int) {
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines, numbers = append(lines, line), append(numbers, i+1)
	}
	return
}

// keyMaterial is the key type and the base64 key of an authorized_keys line,
// without its options (from="…") and comment; empty when the line has none.
func keyMaterial(line string) string {
	var fields []string
	var cur strings.Builder
	quoted := false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !quoted:
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	for i := 0; i+1 < len(fields); i++ {
		if strings.HasPrefix(fields[i+1], "AAAA") && !strings.Contains(fields[i], "=") {
			return fields[i] + " " + fields[i+1]
		}
	}
	return ""
}

// mergeKeys adds the keys of add that current lacks, at the end, and removes
// those of remove, matched by key material; every other line stays, comments
// included. A result without a key is an error.
func mergeKeys(current string, add, remove []string) (string, error) {
	have := map[string]bool{}
	drop := map[string]bool{}
	for _, l := range remove {
		drop[keyMaterial(l)] = true
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(current, "\r", ""), "\n"), "\n") {
		if m := keyMaterial(line); m != "" {
			if drop[m] {
				continue
			}
			have[m] = true
		}
		out = append(out, line)
	}
	if len(out) == 1 && out[0] == "" {
		out = nil
	}
	for _, l := range add {
		if m := keyMaterial(l); !have[m] {
			have[m] = true
			out = append(out, l)
		}
	}
	for _, l := range out {
		if keyMaterial(l) != "" {
			return strings.Join(out, "\n"), nil
		}
	}
	return "", errors.New("that would leave no key at all, which would lock the account out; add the new key first, or replace the list with --authorized-keys / --sudo-keys")
}

// checkKeyFiles reads the key files given and checks each key line with
// ssh-keygen; the replacing files become the JSON string for sops set. The
// flags that replace and the ones that change a list exclude each other.
func checkKeyFiles(x *engine.Ctx) error {
	given := false
	for _, k := range keyKinds {
		replace, change := x.Value(k.replace) != "", x.Value(k.add) != "" || x.Value(k.remove) != ""
		if replace && change {
			return fmt.Errorf("--%s replaces the %s keys, and --%s / --%s change them: give one or the other", k.replace, k.key, k.add, k.remove)
		}
		given = given || replace || change
	}
	if !given {
		return errors.New("give --authorized-keys, --sudo-keys, or --add-… / --remove-… for them")
	}
	for _, k := range keyKinds {
		for _, flag := range []string{k.replace, k.add, k.remove} {
			path := x.Value(flag)
			if path == "" {
				continue
			}
			lines, err := readKeys(x, flag, path, flag != k.remove)
			if err != nil {
				return err
			}
			if flag == k.replace {
				b, _ := os.ReadFile(path)
				x.Values[k.json] = secrets.JSON(string(b))
			}
			x.UI.OK(fmt.Sprintf("%s: %d keys", path, len(lines)))
		}
	}
	return nil
}

// readKeys reads a key file; with check, every key line is checked with
// ssh-keygen, otherwise it only has to hold a key.
func readKeys(x *engine.Ctx, flag, path string, check bool) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", flag, err)
	}
	lines, numbers := keyLines(string(b))
	for i, line := range lines {
		if check {
			if _, err := engine.Cmd("ssh-keygen", "-l", "-f", "/dev/stdin").Stdin(line).StdinNote("the key").RunQuiet(x); err != nil {
				return nil, fmt.Errorf("%s line %d is not an SSH public key: %s", path, numbers[i], line)
			}
		} else if keyMaterial(line) == "" {
			return nil, fmt.Errorf("%s line %d is not an SSH public key: %s", path, numbers[i], line)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s holds no keys; storing an empty list would lock out everyone who relies on it", path)
	}
	return lines, nil
}

// mergeKeyFiles applies the added and removed keys to what the user's file
// holds now, and keeps the result as the JSON the store steps write; a list
// that does not change gets none.
func mergeKeyFiles(x *engine.Ctx) error {
	for _, k := range keyKinds {
		if x.Value(k.add) == "" && x.Value(k.remove) == "" {
			continue
		}
		var add, remove []string
		if p := x.Value(k.add); p != "" {
			add, _ = keyLinesOf(p)
		}
		if p := x.Value(k.remove); p != "" {
			remove, _ = keyLinesOf(p)
		}
		current, err := currentKeys(x, k.key)
		if err != nil {
			return err
		}
		merged, err := mergeKeys(current, add, remove)
		if err != nil {
			return fmt.Errorf("%s keys: %w", k.key, err)
		}
		if strings.TrimRight(merged, "\n") == strings.TrimRight(current, "\n") {
			x.UI.Skip("the " + k.key + " keys stay as they are: nothing to add or remove")
			continue
		}
		x.Values[k.json] = secrets.JSON(merged)
		x.UI.OK(fmt.Sprintf("the %s keys: %d before, now %d", k.key, len(keysIn(current)), len(keysIn(merged))))
	}
	return nil
}

func keyLinesOf(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines, _ := keyLines(string(b))
	return lines, nil
}

func keysIn(text string) []string {
	var keys []string
	for _, l := range strings.Split(text, "\n") {
		if keyMaterial(l) != "" {
			keys = append(keys, l)
		}
	}
	return keys
}

// currentKeys decrypts one key of the user's file in the worktree; a file or
// a key that is not there counts as empty.
func currentKeys(x *engine.Ctx, key string) (string, error) {
	file, err := x.Expand(userFile())
	if err != nil {
		return "", err
	}
	if !x.Options.DryRun {
		if _, err := os.Stat(x.Value("work") + "/" + file); err != nil {
			return "", nil
		}
	}
	res, err := engine.Cmd("sops", "-d", "--extract", `["`+key+`"]`, file).In("{{.work}}").ReadOnly().Quiet().RunQuiet(x)
	if err != nil {
		if strings.Contains(res.Stderr+err.Error(), "not found") {
			return "", nil
		}
		return "", fmt.Errorf("could not read the %s keys stored: %w", key, err)
	}
	return res.Stdout, nil
}
