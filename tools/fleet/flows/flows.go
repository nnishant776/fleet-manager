// Package flows holds fleet's commands, one file each. A flow is a
// declaration: its inputs and its steps. To add one, write it in a new file
// and list it in Menu, or in All if it stays off the menu.
package flows

import "github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"

// Groups describes the intermediate commands.
var Groups = engine.Groups{
	"host":        "Work on one host: check it, its secrets, its enrollment",
	"secrets":     "The secrets repository: hosts, users' passwords and keys (the fleet-secrets port)",
	"inventory":   "A private inventory: each host's own branch",
	"support-key": "The key that opens the support account",
	"trust":       "The keys you trust, kept outside every checkout",
	"repo":        "The tools' own clones of your repositories, by name",
	"user":        "Your own keys and files, for a user who has a branch of their own in the secrets repository",
	"secret":      "Secrets of your own, which your hosts hand only to you",
}

// Menu is what `fleet` offers when run alone, the everyday work first: a
// host, then this machine, then the secrets repository one change at a time.
var Menu = []engine.Section{
	{Name: "Hosts", Flows: []*engine.Flow{HostCheck, HostSync, HostSecret, HostEnroll, SupportKeyRotate, InventoryPin, InventoryImport, InventoryPublish}},
	{Name: "This machine", Flows: []*engine.Flow{Install, InstallPrep, Setup, TrustSigners, RepoAdd, RepoList, RepoSync, Doctor}},
	{Name: "Secrets", Flows: []*engine.Flow{
		SecretsUserPassword, SecretsUserKey, SecretsUserKeys, SecretsUserEnroll, SecretsUserRetire, SecretsHostEnroll, SecretsHostRekey, SecretsHostRetire,
	}},
	{Name: "Your own account", Flows: []*engine.Flow{UserInit, UserPassword, UserKeys, UserSecretSet, UserSecretUnset}},
}

// Every command that takes a checkout or a repository starts by bringing the
// tool's clone up to date when it was given a name (cloneSync, repo.go), and
// every command that reads a checkout then compares its keys with the ones
// you trust (signersCheck, trust.go); fleet trust signers is that comparison
// itself.
func init() {
	for _, f := range All() {
		if f != TrustSigners && hasInput(f, "flake") {
			f.Steps = append([]engine.Step{signersCheck}, f.Steps...)
		}
		if hasInput(f, "flake") || hasInput(f, "repo") {
			f.Steps = append([]engine.Step{cloneSync}, f.Steps...)
		}
	}
}

func hasInput(f *engine.Flow, name string) bool {
	for _, in := range f.Inputs {
		if in.Name == name {
			return true
		}
	}
	return false
}

// All is every flow: the menu's, in its order, then those only typed.
func All() []*engine.Flow {
	var all []*engine.Flow
	for _, s := range Menu {
		all = append(all, s.Flows...)
	}
	return append(all, Docs)
}
