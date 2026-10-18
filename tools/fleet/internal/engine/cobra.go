package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

// Groups describes the intermediate commands, such as "host", by name.
type Groups map[string]string

// Mount adds a command for every flow under root, creating the intermediate
// commands from groups, and makes root itself offer a menu of flows when it
// runs without arguments on a terminal.
func Mount(root *cobra.Command, flows []*Flow, groups Groups) {
	opts := &Options{}
	root.PersistentFlags().BoolVarP(&opts.Yes, "yes", "y", false, "run changes without asking first")
	root.PersistentFlags().BoolVar(&opts.DryRun, "dry-run", false, "show every step and command, run nothing")
	root.PersistentFlags().BoolVar(&opts.Verbose, "verbose", false, "show all of each command's output")

	for _, f := range flows {
		parent := root
		for _, name := range f.Path[:len(f.Path)-1] {
			parent = child(parent, name, groups[name])
		}
		parent.AddCommand(command(f, opts))
	}

	root.RunE = func(cmd *cobra.Command, args []string) error {
		u := ui.New()
		if !u.Interactive() {
			return cmd.Help()
		}
		titles := make([]string, len(flows))
		for i, f := range flows {
			titles[i] = f.Title
		}
		choice, err := u.Choose("What do you want to do?", "fleet <command> --help explains each one.", titles)
		if err != nil {
			return err
		}
		for _, f := range flows {
			if f.Title == choice {
				return runFlow(cmd.Context(), f, *opts, map[string]string{})
			}
		}
		return nil
	}
}

func child(parent *cobra.Command, name, short string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	c := &cobra.Command{Use: name, Short: short}
	parent.AddCommand(c)
	return c
}

func command(f *Flow, opts *Options) *cobra.Command {
	use := f.Path[len(f.Path)-1]
	for _, a := range f.Args {
		use += " [" + strings.ToUpper(a) + "]"
	}
	values := map[string]*string{}
	stdin := map[string]*bool{}
	cmd := &cobra.Command{
		Use:     use,
		Short:   f.Short,
		Long:    f.Long,
		Example: f.Example,
		Args:    cobra.MaximumNArgs(len(f.Args)),
		RunE: func(cmd *cobra.Command, args []string) error {
			given := map[string]string{}
			for name, v := range values {
				if cmd.Flags().Changed(name) {
					given[name] = *v
				}
			}
			for name, v := range stdin {
				if *v {
					given[name+"-stdin"] = "true"
				}
			}
			for i, a := range args {
				if _, dup := given[f.Args[i]]; dup {
					return fmt.Errorf("%s given both as an argument and as --%s", f.Args[i], f.Args[i])
				}
				given[f.Args[i]] = a
			}
			return runFlow(cmd.Context(), f, *opts, given)
		},
	}
	for _, in := range f.Inputs {
		help := in.Prompt
		if in.Help != "" {
			help += ". " + in.Help
		}
		if in.Secret {
			b := false
			stdin[in.Name] = &b
			cmd.Flags().BoolVar(&b, in.Name+"-stdin", false, "read "+in.Name+" from standard input ("+strings.ToLower(in.Prompt)+")")
			continue
		}
		s := ""
		values[in.Name] = &s
		cmd.Flags().StringVar(&s, in.Name, "", help)
	}
	return cmd
}

func runFlow(ctx context.Context, f *Flow, opts Options, flags map[string]string) error {
	x := &Ctx{
		Context: ctx,
		UI:      ui.New(),
		Runner:  Exec{},
		Options: opts,
		Values:  map[string]string{},
		Stdin:   os.Stdin,
		flags:   flags,
	}
	err := Run(x, f)
	if errors.Is(err, ErrStopped) {
		return nil
	}
	return err
}

// RunWith runs a flow with an explicit runner and UI, for tests.
func RunWith(x *Ctx, f *Flow, flags map[string]string) error {
	x.flags = flags
	return Run(x, f)
}
