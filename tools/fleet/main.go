// fleet walks the maintainer through this fleet's procedures: installing and
// enrolling machines, secrets, users and keys, and checking hosts. Every step
// says what it will do and why, shows the exact command, asks before changing
// anything, and checks the result. Run it without arguments for a menu.
package main

import (
	"context"
	"image/color"
	"io"
	"os"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/nnishant776/fleet-manager/tools/fleet/flows"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// version is set at build time (-X main.version=…).
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "fleet",
		Short: "Guided procedures for this fleet: hosts, secrets, users and keys",
		Long: "fleet walks you through this fleet's procedures one step at a time. Each step\n" +
			"says what it will do and why, shows the exact command, asks before changing\n" +
			"anything, and checks the result. Run it without arguments for a menu.\n\n" +
			"Every question has a flag, so any command also runs without prompts; --yes\n" +
			"skips the confirmations, and --dry-run shows every command and changes nothing.",
		SilenceUsage: true,
	}
	engine.Mount(root, flows.All(), flows.Groups)

	err := fang.Execute(context.Background(), root,
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(scheme),
		fang.WithErrorHandler(errorHandler),
		fang.WithNotifySignal(os.Interrupt),
	)
	if err != nil {
		os.Exit(1)
	}
}

// scheme keeps the help pages in fleet's palette: one accent, dim comments,
// and no background behind the usage block.
func scheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	base := c(lipgloss.Black, lipgloss.White)
	return fang.ColorScheme{
		Base:           base,
		Title:          ui.Accent,
		Description:    base,
		Codeblock:      lipgloss.NoColor{},
		Program:        ui.Accent,
		DimmedArgument: ui.Dim,
		Comment:        ui.Dim,
		Flag:           ui.Accent,
		FlagDefault:    ui.Dim,
		Command:        ui.Accent,
		QuotedString:   base,
		Argument:       base,
		Help:           base,
		Dash:           ui.Dim,
		ErrorHeader:    [2]color.Color{ui.Bad, lipgloss.NoColor{}},
		ErrorDetails:   ui.Bad,
	}
}

// errorHandler prints errors the way failed steps look.
func errorHandler(w io.Writer, _ fang.Styles, err error) {
	_, _ = lipgloss.Fprintln(w, lipgloss.NewStyle().Foreground(ui.Bad).Render("✗ "+err.Error()))
}
