package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/spf13/cobra"
)

// runFlags are the flags every run command takes beside a kind's params.
type runFlags struct {
	options telltale.StartOptions
}

// bind defines the flags every run command takes on cmd.
func (f *runFlags) bind(cmd *cobra.Command) {
	f.options.WorkDir = "."
	cmd.Flags().StringVar(&f.options.WorkDir, "work-dir", f.options.WorkDir, "Directory holding .telltale/traces")
	cmd.Flags().DurationVar(&f.options.Duration, "duration", 0, "Stop the run after this long, e.g. 30s (0 runs until the command exits or Ctrl+C)")
	cmd.Flags().DurationVar(&f.options.Every, "every", 0, "Read the checkpointed captures this often, e.g. 10s")
}

// newRunCommand is `run` with one subcommand per registered trace kind, each
// with its params as flags.
func newRunCommand() *cobra.Command {
	run := &cobra.Command{
		Use:   "run <kind> [flags] [-- command [args...]]",
		Short: "Trace a command, or a running process, with one trace kind",
		Args:  cobra.NoArgs,
	}
	setHelp(run, func(cmd *cobra.Command) api.Text { return runHelp(cmd).Text })
	kinds, err := traces.Kinds()
	if err != nil {
		panic(fmt.Sprintf("trace kind catalog: %v", err))
	}
	for _, registration := range traces.Registrations() {
		plugin, err := traces.Lookup(kinds, registration.Name)
		if err != nil {
			panic(err)
		}
		run.AddCommand(newRunKindCommand(registration, plugin))
	}
	return run
}

func newRunKindCommand(registration traces.Registration, plugin tracing.TracePlugin) *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   registration.Name + " [flags] [-- command [args...]]",
		Short: registration.Summary,
		Args:  cobra.ArbitraryArgs,
	}
	cmd.Flags().SetInterspersed(false)
	flags.bind(cmd)
	cmd.Flags().IntVar(&flags.options.PID, "pid", 0, "Attach to this running process instead of running a command")
	schema, err := plugin.Params()
	if err != nil {
		panic(fmt.Sprintf("trace kind %s params: %v", registration.Name, err))
	}
	params, err := bindParamFlags(cmd.Flags(), schema)
	if err != nil {
		panic(fmt.Sprintf("trace kind %s: %v", registration.Name, err))
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		raw, err := params.params()
		if err != nil {
			return err
		}
		if err := plugin.ValidateParams(raw); err != nil {
			return fmt.Errorf("%s %w", registration.Name, err) // ValidateParams says "params: ..."
		}
		options := flags.options
		if options.Command, err = commandArgs(options.PID, args, registration.Target); err != nil {
			return err
		}
		options.Traces = []telltale.TraceSpec{{Kind: registration.Name, Params: raw}}
		return runTraced(cmd, options, telltale.Start)
	}
	setHelp(cmd, func(cmd *cobra.Command) api.Text { return kindHelp(cmd, registration).Text })
	return cmd
}

// commandArgs are the command a run starts: args, which an attached run
// (pid) must not have and any other must, but a run of a target observing
// the host, which may go without.
func commandArgs(pid int, args []string, target traces.Target) ([]string, error) {
	switch {
	case pid != 0 && len(args) > 0:
		return nil, fmt.Errorf("attach to --pid %d or run %q, not both", pid, strings.Join(args, " "))
	case pid == 0 && len(args) == 0 && target != traces.TargetHost:
		return nil, errors.New("give a command after --, or --pid to attach to a running process")
	}
	return args, nil
}

// runTraced starts a run with start, waits for it and prints what it
// recorded; a command that failed or a checkpoint that did is an error.
func runTraced(cmd *cobra.Command, options telltale.StartOptions, start func(context.Context, telltale.StartOptions) (*telltale.Run, error)) error {
	if options.Stdin == nil && options.Stdout == nil && options.Stderr == nil && options.PID == 0 {
		options.Stdin, options.Stdout, options.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	}
	connections, err := telltale.OpenConnections()
	if err != nil {
		return err
	}
	options.Connections = connections
	started, err := start(cmd.Context(), options)
	if err != nil {
		return err
	}
	switch {
	case options.PID != 0:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Tracing pid %d into %s; Ctrl+C stops\n", options.PID, started.Dir()) // a lost notice loses nothing recorded
	case len(options.Command) == 0:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Recording the host into %s; Ctrl+C stops\n", started.Dir())
	}
	result, err := started.Wait()
	if err != nil {
		return err
	}
	clicky.MustPrint(result)
	switch {
	case result.ExitCode != 0:
		return fmt.Errorf("%s exited with code %d", options.Command[0], result.ExitCode)
	case len(result.CheckpointErrors) > 0:
		return fmt.Errorf("%d checkpoints failed: %s", len(result.CheckpointErrors), strings.Join(result.CheckpointErrors, "; "))
	}
	return nil
}

// parsePID reads a pid argument.
func parsePID(value string) (int, error) {
	pid, err := strconv.Atoi(value)
	if err != nil || pid < 1 {
		return 0, fmt.Errorf("%q is not a pid", value)
	}
	return pid, nil
}
