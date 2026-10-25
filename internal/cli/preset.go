package cli

import (
	"errors"
	"fmt"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale"
	"github.com/spf13/cobra"
)

// newPresetCommand is `telltale <preset> PID | -- command`.
func newPresetCommand(preset telltale.Preset) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   preset.Name + " [flags] PID | -- command [args...]",
		Short: preset.Summary,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			options := flags.options
			var err error
			if options.PID, options.Command, err = presetTarget(args, cmd.ArgsLenAtDash()); err != nil {
				return fmt.Errorf("%s: %w", preset.Name, err)
			}
			return runTraced(cmd, options, preset.Start)
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&flags.options.Facets, "facets", false, "Read every process's facets and environment into the process tree")
	setHelp(cmd, func(cmd *cobra.Command) api.Text { return presetHelp(cmd, preset).Text })
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
