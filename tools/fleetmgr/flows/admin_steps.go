package flows

import (
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
)

// knowHostKeyStep makes sure this machine knows the host's key before any
// step logs in as its admin account.
var knowHostKeyStep = engine.Step{
	Title: "Know {{.host}}'s host key",
	Why: "Every login checks it, so that nothing between here and there can answer in the host's place. " +
		"A key this machine does not know yet is fetched, and recorded only once its fingerprint matches the one on the host's console.",
	Required: true,
	Do:       engine.Func(fleetmgr.KnowHostKey),
}

// adminSudoStep settles how the admin account's sudo works, before any step
// relies on it.
var adminSudoStep = engine.Step{
	Title: "Check {{.admin}}'s sudo",
	Why: "Without a password, by the key in the agent or a rule, nothing more is needed. Otherwise its password is asked for once, " +
		"checked with a single attempt, kept in memory only, and handed to sudo on the host on stdin.",
	Required: true,
	Do:       engine.Func(fleetmgr.AdminSudo),
}
