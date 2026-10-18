// Package flows holds fleet's commands, one file each. A flow is a
// declaration: its inputs and its steps. To add one, write it in a new file
// and list it in All.
package flows

import "github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"

// Groups describes the intermediate commands.
var Groups = engine.Groups{}

// All is every flow, in the order the menu shows them.
func All() []*engine.Flow {
	return []*engine.Flow{
		Doctor,
	}
}
