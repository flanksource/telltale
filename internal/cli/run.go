package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/spf13/cobra"
)

// runFlags are the flags every run command takes beside a kind's params.
type runFlags struct {
	options telltale.StartOptions
}

// bind defines the flags every run command takes on cmd; --every only when
// the run reads checkpointed captures, as it is how often they are read. A
// kind without checkpoints may then take --every as its own param, as the
// query kind's interval does.
func (f *runFlags) bind(cmd *cobra.Command, checkpointed bool) {
	bindDataDir(cmd.Flags(), &f.options.DataDir)
	cmd.Flags().DurationVar(&f.options.Duration, "duration", 0, "Stop the run after this long, e.g. 30s (0 runs until the command exits or Ctrl+C)")
	if checkpointed {
		cmd.Flags().DurationVar(&f.options.Every, "every", 0, "Read the checkpointed captures this often, e.g. 10s")
	}
	bindFollow(cmd)
}

// newRunCommand is `run` with one subcommand per registered trace kind, each
// with its params as flags.
func newRunCommand() *cobra.Command {
	var flags runFlags
	var ref string
	run := &cobra.Command{
		Use:   "run <kind> [flags] [-- command [args...]]",
		Short: "Trace a command, or a running process, with one trace kind or a trace definition",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ref == "" && len(args) > 0 {
				return fmt.Errorf("unknown trace kind %q: see telltale run --help, or name a definition with --definition", args[0])
			}
			if ref == "" {
				return cmd.Help()
			}
			options := flags.options
			options.Command = args
			return runDefinition(cmd, ref, options)
		},
	}
	flags.bind(run, true)
	run.Flags().IntVar(&flags.options.PID, "pid", 0, "With --definition, attach to this running process instead of running a command")
	run.Flags().StringVar(&ref, "definition", "", "Arm the captures of this trace definition: a stored name, or a .yaml file")
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
	flags.bind(cmd, registration.Checkpoint != nil)
	schema, err := plugin.Params()
	if err != nil {
		panic(fmt.Sprintf("trace kind %s params: %v", registration.Name, err))
	}
	params, err := bindParamFlags(cmd.Flags(), schema)
	if err != nil {
		panic(fmt.Sprintf("trace kind %s: %v", registration.Name, err))
	}
	var checkpoint *profileCheckpointFlags
	if defaults, ok := registration.Checkpoint.(goprof.CheckpointParams); ok {
		checkpoint = bindProfileCheckpoint(cmd.Flags(), defaults)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		raw, err := params.params()
		if err != nil {
			return err
		}
		options := flags.options
		if cmd.Flags().Lookup("pid") != nil {
			options.PID, err = cmd.Flags().GetInt("pid")
			if err != nil {
				return err
			}
		}
		if options.PID == 0 {
			if err := plugin.ValidateParams(raw); err != nil {
				return fmt.Errorf("trace %s %w", registration.Name, err)
			}
		}
		options.Command = args
		var reading []byte
		if checkpoint != nil {
			reading, err = checkpoint.params()
			if err != nil {
				return err
			}
		}
		options.Traces = []telltale.TraceSpec{{Kind: registration.Name, Params: raw, Checkpoint: reading}}
		return runTraced(cmd, options, telltale.Start)
	}
	setHelp(cmd, func(cmd *cobra.Command) api.Text { return kindHelp(cmd, registration).Text })
	return cmd
}

// runTraced starts a run with start, waits for it and prints what it
// recorded; a command that failed or a checkpoint that did is an error.
func runTraced(cmd *cobra.Command, options telltale.StartOptions, start func(context.Context, telltale.StartOptions) (*telltale.Run, error)) error {
	follow, tui, ui, sort, err := followOptions(cmd)
	if err != nil {
		return err
	}
	format := clicky.Flags.ResolveFormat()
	if tui && format == "ndjson" {
		return fmt.Errorf("--tui cannot combine with ndjson")
	}
	if follow && format != "ndjson" && format != "pretty" && format != "ansi" {
		return fmt.Errorf("follow supports ndjson, ansi or pretty output")
	}
	if options.Stdin == nil && options.Stdout == nil && options.Stderr == nil && options.PID == 0 {
		options.Stdin, options.Stdout, options.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		if follow && format == "ndjson" {
			options.Stdout = cmd.ErrOrStderr()
		}
	}
	if tui {
		options.Stdin, options.Stdout, options.Stderr = nil, nil, nil
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
	var viewer *server.Viewer
	if ui {
		viewer, err = openRunViewer(cmd, server.ViewerOptions{DataDir: options.DataDir, RunIDs: []string{started.ID()}})
		if err != nil {
			return errors.Join(err, started.Stop())
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = viewer.Close(ctx)
		}()
	}
	switch {
	case options.PID != 0:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Tracing pid %d into %s; Ctrl+C stops\n", options.PID, started.Dir()) // a lost notice loses nothing recorded
	case len(options.Command) == 0:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Recording the host into %s; Ctrl+C stops\n", started.Dir())
	}
	var result *telltale.Result
	if tui {
		result, err = showTraceTUI(cmd.Context(), started, sort)
	} else {
		result, err = read.Wait(cmd.Context(), started, follow, func(record read.Record) error { return printTraceRecord(cmd, record) })
	}
	if err != nil {
		return err
	}
	if follow {
		output, err := clicky.Format(result, clicky.FormatOptions{Format: "pretty"})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), output); err != nil {
			return err
		}
	} else {
		clicky.MustPrint(result)
	}
	if viewer != nil {
		if err := waitViewer(cmd.Context(), viewer); err != nil {
			return err
		}
	}
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
