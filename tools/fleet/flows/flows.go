// Package flows holds fleet's commands, one file each. A flow is a
// declaration: its inputs and its steps. To add one, write it in a new file
// and list it in All.
package flows

import "github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"

// Groups describes the intermediate commands.
var Groups = engine.Groups{
	"host":        "Work on one host: check it, its secrets, its enrollment",
	"secrets":     "The secrets repository: hosts, users' passwords and keys (the fleet-secrets port)",
	"support-key": "The key that opens the support account",
}

// All is every flow, in the order the menu shows them.
func All() []*engine.Flow {
	return []*engine.Flow{
		HostCheck,
		HostSecret,
		HostEnroll,
		SecretsHostEnroll,
		SecretsHostRekey,
		SecretsHostRetire,
		SecretsUserPassword,
		SecretsUserKey,
		SupportKeyRotate,
		Setup,
		Install,
		Docs,
		Doctor,
	}
}
