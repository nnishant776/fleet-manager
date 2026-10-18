package flows

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// Tool is a program fleet runs, and where to get it.
type Tool struct {
	Name     string
	Package  string // in nixpkgs
	Optional bool   // only some flows need it
	For      string
}

// Tools are every program a flow may run. The Nix package `fleet` puts all of
// them but nix on PATH; the bare `fleet-static` relies on the machine's own.
var Tools = []Tool{
	{Name: "git", Package: "git", For: "both repositories"},
	{Name: "ssh", Package: "openssh", For: "reaching hosts"},
	{Name: "ssh-agent", Package: "openssh", For: "the one-key agent for admin steps"},
	{Name: "ssh-add", Package: "openssh", For: "the one-key agent for admin steps"},
	{Name: "ssh-keygen", Package: "openssh", For: "keys and signatures"},
	{Name: "sops", Package: "sops", For: "encrypting secrets"},
	{Name: "age", Package: "age", For: "the recovery identity"},
	{Name: "age-keygen", Package: "age", For: "the recovery identity"},
	{Name: "yq", Package: "yq-go", For: "fleet.yaml"},
	{Name: "mkpasswd", Package: "mkpasswd", For: "password hashes"},
	{Name: "nix", Package: "nix", For: "reading the configuration"},
	{Name: "gh", Package: "gh", Optional: true, For: "adding deploy keys; without it fleet shows the web steps"},
}

// Doctor reports which of the tools are on PATH.
var Doctor = &engine.Flow{
	Path:  []string{"doctor"},
	Title: "Check this machine has the tools fleet uses",
	Short: "Check this machine has the tools fleet uses",
	Long: "Lists every program fleet runs and whether it is on PATH, with the nixpkgs\n" +
		"package that provides each missing one. Installed through Nix, fleet brings\n" +
		"all of them but nix itself; the bare static binary relies on the machine's own.",
	Steps: []engine.Step{
		{
			Title: "Tools on PATH",
			Why:   "fleet shows and runs the same commands you could type, so it needs these programs.",
			Do: engine.Func(func(x *engine.Ctx) error {
				missing := 0
				for _, t := range Tools {
					path, err := exec.LookPath(t.Name)
					switch {
					case err == nil:
						x.UI.OK(fmt.Sprintf("%-10s %s", t.Name, path))
					case t.Optional:
						x.UI.Skip(fmt.Sprintf("%-10s not found (optional: %s); nix shell nixpkgs#%s", t.Name, t.For, t.Package))
					default:
						missing++
						x.UI.Fail(fmt.Sprintf("%-10s not found (%s); nix shell nixpkgs#%s", t.Name, t.For, t.Package))
					}
				}
				if missing > 0 {
					return errors.New("required tools are missing")
				}
				return nil
			}),
		},
	},
}
