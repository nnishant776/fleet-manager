// Package flows holds fleet's commands, one file each. A flow is a
// declaration: its inputs and its steps. To add one, write it in a new file
// and list it in Menu, or in All if it stays off the menu.
package flows

import "github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"

// Groups describes the intermediate commands.
var Groups = engine.Groups{
	"host":        "Work on one host: check it, its secrets, its enrollment",
	"secrets":     "The secrets repository: hosts, users' passwords and keys (the fleet-secrets port)",
	"support-key": "The key that opens the support account",
	"trust":       "The keys you trust, kept outside every checkout",
}

// Menu is what `fleet` offers when run alone, the everyday work first: a
// host, then this machine, then the secrets repository one change at a time.
var Menu = []engine.Section{
	{Name: "Hosts", Flows: []*engine.Flow{HostCheck, HostSecret, HostEnroll, SupportKeyRotate}},
	{Name: "This machine", Flows: []*engine.Flow{Install, InstallPrep, Setup, TrustSigners, Doctor}},
	{Name: "Secrets", Flows: []*engine.Flow{
		SecretsUserPassword, SecretsUserKey, SecretsHostEnroll, SecretsHostRekey, SecretsHostRetire,
	}},
}

// All is every flow: the menu's, in its order, then those only typed.
func All() []*engine.Flow {
	var all []*engine.Flow
	for _, s := range Menu {
		all = append(all, s.Flows...)
	}
	return append(all, Docs)
}
