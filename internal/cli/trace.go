package cli

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale/internal/tracerun"
	"github.com/spf13/cobra"
)

func newTraceCommand() *cobra.Command {
	trace := &cobra.Command{Use: "trace", Short: "Record what a command does"}
	options := tracerun.Options{WorkDir: "."}
	var specs []string
	run := &cobra.Command{
		Use:   "run [flags] -- command [args...]",
		Short: "Run a command under trace, recording its traffic and process tree into .telltale/traces",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.Command = args
			options.Stdin, options.Stdout, options.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			options.Traces = options.Traces[:0]
			for _, value := range specs {
				spec, err := tracerun.ParseSpec(value)
				if err != nil {
					return err
				}
				options.Traces = append(options.Traces, spec)
			}
			result, err := tracerun.Run(cmd.Context(), options)
			if err != nil {
				return err
			}
			clicky.MustPrint(result)
			if result.ExitCode != 0 {
				return fmt.Errorf("%s exited with code %d", args[0], result.ExitCode)
			}
			return nil
		},
	}
	run.Flags().StringArrayVar(&specs, "trace", []string{"http_proxy"}, `Capture to arm, as kind or kind:{json params}, e.g. http_proxy:{"mode":"mitm"}`)
	run.Flags().BoolVar(&options.Facets, "facets", false, "Read every process's facets and environment into the process tree")
	run.Flags().StringVar(&options.WorkDir, "work-dir", options.WorkDir, "Directory holding .telltale/traces")
	trace.AddCommand(run)
	return trace
}
