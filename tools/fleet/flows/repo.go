package flows

import (
	"fmt"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// cloneSync starts every command that takes a checkout or a repository (see
// flows.go), ahead of the keys comparison: when the repository was given by
// name, the command works in the tool's own clone of it, which is brought up
// to date here.
var cloneSync = engine.Step{
	Title: "Bring the tool's clone up to date",
	Why: "You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken " +
		"only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.",
	Required: true,
	When: func(x *engine.Ctx) (bool, string) {
		if x.Value("flake_name") != "" || x.Value("repo_name") != "" {
			return true, ""
		}
		return false, "a checkout or flake given by path"
	},
	Do: engine.Func(func(x *engine.Ctx) error {
		for _, in := range []string{"flake", "repo"} {
			name := x.Value(in + "_name")
			if name == "" {
				continue
			}
			dir := x.Value(in)
			if !fileExists(dir) {
				return fmt.Errorf("the clone of %s is not in %s: run fleet repo add again with its address", name, dir)
			}
			x.UI.OK(name + " is the tool's clone in " + dir)
			if err := fleet.SyncClone(x, dir); err != nil {
				return err
			}
		}
		return nil
	}),
}
