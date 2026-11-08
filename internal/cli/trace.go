package cli

import (
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/spf13/cobra"
)

func newTraceCommand() *cobra.Command {
	trace := &cobra.Command{Use: "trace", Short: "Record what a command does"}
	trace.AddCommand(newTraceRunCommand(), newTraceListCommand(), newTraceReplayCommand())
	return trace
}

func newTraceRunCommand() *cobra.Command {
	options := telltale.StartOptions{}
	var specs []string
	run := &cobra.Command{
		Use:   "run [flags] -- command [args...]",
		Short: "Run a command under trace, recording its traffic and process tree into the data dir",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.Command = args
			var err error
			if options.Traces, err = parseTraceSpecs(specs); err != nil {
				return err
			}
			return runTraced(cmd, options, telltale.Start)
		},
	}
	run.Flags().StringArrayVar(&specs, "trace", []string{`http:{"target":{"seam":"proxy"}}`}, `Capture to arm, as kind or kind:{json params}, e.g. http:{"target":{"seam":"proxy"},"mode":"mitm"}`)
	run.Flags().BoolVar(&options.Tree.Facets, "facets", false, "Read every process's facets and environment into the process tree")
	bindDataDir(run.Flags(), &options.DataDir)
	run.Flags().DurationVar(&options.Duration, "duration", 0, "Stop the command after this long, e.g. 30s")
	run.Flags().DurationVar(&options.Every, "every", 0, "Read the checkpointed captures this often, e.g. 10s")
	bindFollow(run)
	setHelp(run, func(cmd *cobra.Command) api.Text { return traceRunHelp(cmd).Text })
	return run
}

// parseTraceSpecs reads each --trace, refusing a kind the catalog does not
// serve or params it would refuse before any run store is made.
func parseTraceSpecs(values []string) ([]telltale.TraceSpec, error) {
	kinds, err := traces.Kinds()
	if err != nil {
		return nil, err
	}
	specs := make([]telltale.TraceSpec, 0, len(values))
	for _, value := range values {
		spec, err := telltale.ParseTraceSpec(value)
		if err != nil {
			return nil, err
		}
		if err := traces.Validate(kinds, spec.Kind, spec.Params); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func newTraceListCommand() *cobra.Command {
	options := telltale.ListOptions{}
	var statuses []string
	var since time.Duration
	list := &cobra.Command{
		Use:     "list",
		Short:   "List the runs recorded under the data dir, newest first",
		Long:    "List the runs recorded under the data dir, newest first, then the standing stores of the data dir. The OTLP receiver's store is the pseudo-run receiver: running while telltale serve --otlp writes it, standing otherwise. Read it like a run with telltale records spans --run receiver.",
		Example: "  telltale trace list --status failed --since 2h\n  telltale trace list --kind spans\n  telltale trace list --status running,standing",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			options.Status = options.Status[:0]
			for _, value := range statuses {
				status, err := telltale.ParseStatus(value)
				if err != nil {
					return err
				}
				options.Status = append(options.Status, status)
			}
			if since > 0 {
				options.Since = time.Now().Add(-since)
			}
			runs, err := telltale.List(options)
			if err != nil {
				return err
			}
			clicky.MustPrint(runs)
			return nil
		},
	}
	bindDataDir(list.Flags(), &options.DataDir)
	list.Flags().StringVarP(&options.Query, "query", "q", "", "Keep runs whose id or command contains this text, ignoring case")
	list.Flags().StringSliceVar(&statuses, "status", nil, fmt.Sprintf("Keep runs in these statuses %v", telltale.Statuses))
	list.Flags().StringVar(&options.Kind, "kind", "", "Keep runs that armed a capture of this trace kind")
	list.Flags().DurationVar(&since, "since", 0, "Keep runs started within this long ago, e.g. 2h")
	list.Flags().IntVar(&options.Limit, "limit", 0, "Return at most this many runs (0 for all)")
	return list
}
