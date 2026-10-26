package flows

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// declaredSecret is what fleet-secrets-sync check reads of each secret a
// configuration declares.
type declaredSecret struct {
	SopsFile string `json:"sopsFile"`
	Key      string `json:"key"`
}

// checkBranchHoldsSecrets checks that the host's branch of the secrets
// repository holds every secret the configuration it is about to run
// declares, the check fleet-secrets-sync makes on the host after each sync,
// but before the switch, when a miss can still be put right from here. It
// reads the files as they are in git: sops keeps the keys of an encrypted file
// in clear, so nothing is decrypted.
func checkBranchHoldsSecrets(x *engine.Ctx) error {
	host := x.Value("host")
	ref := secrets.BranchRef(x, host)
	if ref == "" {
		return fmt.Errorf("there is no host/%s in %s, local or fetched from origin: enroll it first, or fetch", host, x.Value("repo"))
	}
	flake, err := x.Expand(x.Value("eval_flake"))
	if err != nil {
		return err
	}
	if flake == "" {
		flake = x.Value("flake")
	}
	attr := flake + "#nixosConfigurations." + host + ".config."
	state, err := engine.Cmd("nix", "eval", "--raw", attr+"conf.secrets.statePath").ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	prefix := strings.TrimSpace(state.Stdout) + "/secrets/"
	res, err := engine.Cmd("nix", "eval", "--json", attr+"sops.secrets",
		"--apply", "builtins.mapAttrs (_: s: { inherit (s) sopsFile key; })").ReadOnly().Quiet().Run(x)
	if err != nil {
		return err
	}
	var declared map[string]declaredSecret
	if x.Options.DryRun && strings.TrimSpace(res.Stdout) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(res.Stdout), &declared); err != nil {
		return fmt.Errorf("could not read the secrets the configuration declares: %w", err)
	}

	docs := map[string]string{} // path in the branch -> its content, "" when absent
	read := func(path string) string {
		if doc, ok := docs[path]; ok {
			return doc
		}
		out, err := engine.Cmd("git", "show", ref+":"+path).In("{{.repo}}").ReadOnly().Quiet().RunQuiet(x)
		if err != nil {
			out.Stdout = ""
		}
		docs[path] = out.Stdout
		return out.Stdout
	}

	var missing, hints []string
	add := func(line, hint string) {
		missing = append(missing, line)
		for _, h := range hints {
			if h == hint {
				return
			}
		}
		hints = append(hints, hint)
	}
	checked := 0
	for _, name := range sortedKeys(declared) {
		s := declared[name]
		path, ok := strings.CutPrefix(s.SopsFile, prefix)
		if !ok {
			continue // not a file of the secrets repository
		}
		checked++
		doc := read(path)
		if doc == "" {
			add("missing "+path+" (for "+name+")", addCommand(host, path, s.Key))
			continue
		}
		if s.Key == "" {
			continue
		}
		has, err := keyPresent(x, doc, s.Key)
		if err != nil {
			return err
		}
		if !has {
			add("missing key "+s.Key+" in "+path+" (for "+name+")", addCommand(host, path, s.Key))
		}
	}
	if len(missing) > 0 {
		for _, m := range missing {
			x.UI.Fail(m)
		}
		return fmt.Errorf("host/%s lacks %d of the secrets the configuration declares, and %s would not start them. Add them, then run this again:\n  %s",
			host, len(missing), host, strings.Join(hints, "\n  "))
	}
	x.UI.OK(fmt.Sprintf("host/%s holds the %d secrets the configuration declares from the secrets repository", host, checked))
	return nil
}

// keyPresent says whether a sops file holds the key, a / nesting it as
// sops-nix does.
func keyPresent(x *engine.Ctx, doc, key string) (bool, error) {
	expr := "."
	for _, part := range strings.Split(key, "/") {
		q, _ := json.Marshal(part)
		expr += "[" + string(q) + "]"
	}
	x.Values["branch_doc"] = doc
	res, err := engine.Cmd("yq", expr+" != null").StdinFrom("branch_doc").StdinNote("the file as it is on the branch").RunQuiet(x)
	if err != nil {
		return false, fmt.Errorf("could not read the file: %w", err)
	}
	return strings.TrimSpace(res.Stdout) == "true", nil
}

// addCommand is the command that stores what is missing: the secret in a
// file of the host, or in a user's file on it.
func addCommand(host, path, key string) string {
	parts := strings.Split(strings.TrimSuffix(path, ".yaml"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "users" && parts[2] == host:
		user := parts[1]
		switch key {
		case "authorized_keys":
			return "fleet secrets user-keys " + user + " " + host + " --authorized-keys <file of public keys>"
		case "sudo_keys":
			return "fleet secrets user-keys " + user + " " + host + " --sudo-keys <file of public keys>"
		case "password":
			return "fleet secrets user-password " + user + " " + host
		case "ssh_key", "ssh_key_pub":
			return "fleet secrets user-key " + user + " " + host + " (after user-password, which creates the file)"
		}
		return "add " + key + " to users/" + user + "/" + host + ".yaml on host/" + host
	case len(parts) == 2 && parts[0] == "hosts" && parts[1] == host && key != "":
		return "fleet host secret " + host + " " + key
	}
	return "add " + key + " to " + path + " on host/" + host
}

func sortedKeys(m map[string]declaredSecret) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
