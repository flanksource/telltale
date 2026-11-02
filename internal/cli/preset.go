package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/spf13/cobra"
)

// newPresetCommand is `telltale <definition> PID | -- command`, a built-in
// trace definition run against a process or a command.
func newPresetCommand(def *definition.TraceDefinition) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   def.Name + " [flags] PID | -- command [args...]",
		Short: def.Summary(),
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			options := flags.options
			var err error
			if options.PID, options.Command, err = presetTarget(args, cmd.ArgsLenAtDash()); err != nil {
				return fmt.Errorf("%s: %w", def.Name, err)
			}
			return runTraced(cmd, options, func(ctx context.Context, options telltale.StartOptions) (*telltale.Run, error) {
				applied, err := definition.Apply(def, options)
				if err != nil {
					return nil, err
				}
				return telltale.Start(ctx, applied)
			})
		},
	}
	flags.bind(cmd, true)
	cmd.Flags().BoolVar(&flags.options.Tree.Facets, "facets", false, "Read every process's facets and environment into the process tree")
	setHelp(cmd, func(cmd *cobra.Command) api.Text { return presetHelp(cmd, def).Text })
	return cmd
}

// presetTarget is the pid a preset attaches to, the one argument before any
// --, or the command after it.
func presetTarget(args []string, dash int) (int, []string, error) {
	switch {
	case dash > 0:
		return 0, nil, fmt.Errorf("attach to pid %s or run %q, not both", args[0], args[dash:])
	case dash == 0 && len(args) == 0:
		return 0, nil, errors.New("give a command after --")
	case dash == 0:
		return 0, args, nil
	case len(args) != 1:
		return 0, nil, errors.New("give the pid of a running process, or -- command [args...]")
	}
	pid, err := parsePID(args[0])
	return pid, nil, err
}
