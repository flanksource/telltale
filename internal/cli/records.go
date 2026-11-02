package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newRecordsCommand() *cobra.Command {
	scope := read.Scope{}
	var q read.Query
	var since, from, until, groupBy, view string
	var cols []string
	var follow bool
	cmd := &cobra.Command{
		Use: "records <kind>", Short: "Read, follow, or aggregate a run's stored records", Args: cobra.ExactArgs(1),
		Long: `Read stored trace records through the kind's query profile. By default, select the newest run containing the kind and return up to 100 rows in profile order.

--since accepts a duration (2h) or RFC3339 timestamp: select all runs started at or after that instant and use it as the row-time lower bound. Standing stores, such as the OTLP receiver's (--run receiver), are continuous and always selectable. --from overrides only the row window; --until is exclusive. --run and --runs narrow the run selection. --follow requires a single selected run and ends when its streams seal.

--filter uses profile column grammar, e.g. pid=123 or rssBytes=>=1048576. --where uses CEL with a row binding, e.g. row.cpuPercent != null && row.cpuPercent > 10. --limit applies after CEL; --all reads every matching row. A continuation cursor is printed to stderr when more rows remain.

--stats aggregates all matching rows unless --limit is explicitly set. Nulls are skipped. Percentiles use nearest rank, exact through 8192 values per column/group, then an 8192-value reservoir; approximate=true identifies sampling. At most 1024 groups, 64 columns, and 1024 column/group pairs are allowed (64 MiB of reservoir values). --group-by alone counts rows. --view runs a named kind view over the seq window; row filters and time bounds require --stats instead.`,
		Example: `  telltale records process_samples --json
  telltale records process_samples --since 2h --filter 'rssBytes=>=1048576' --where 'row.cpuPercent > 10' --all
  telltale records process_samples --run run-2026-10-28T09-00-00Z --follow --format ndjson
  telltale records process_samples --stats cpuPercent,rssBytes --group-by pid
  telltale records process_samples --group-by pid --format csv
  telltale records spans --run receiver --filter service=checkout --limit 5
  telltale records spans --run receiver --stats durationMs --group-by operation
  telltale records spans --run receiver --follow
  telltale records sql --view repeats`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			scope, q, aggregate, err := read.ParseRequest(scope.DataDir, recordValues(cmd))
			if err != nil {
				return err
			}
			selected, err := read.Resolve(args[0], scope)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, selected.Close()) }()
			if aggregate.Follow {
				return read.FollowSelection(cmd.Context(), selected.Refs, q, recordPrinter(cmd))
			}
			if aggregate.View != "" {
				rows := []query.Row{}
				for _, ref := range selected.Refs {
					ref.AfterSeq = max(ref.AfterSeq, q.AfterSeq)
					if q.ToSeq > 0 && (ref.ToSeq == 0 || q.ToSeq < ref.ToSeq) {
						ref.ToSeq = q.ToSeq
					}
					part, err := read.View(cmd.Context(), ref, aggregate.View)
					if err != nil {
						return err
					}
					for _, row := range part {
						row["runId"] = ref.RunID
						rows = append(rows, row)
					}
				}
				return printRecords(cmd, rows)
			}
			q.NextCursor = func(cursor string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Next cursor:", cursor) }
			rows := read.Rows(cmd.Context(), selected.Refs, q)
			if aggregate.Enabled() {
				stats, err := read.Stats(rows, aggregate.Columns, aggregate.GroupBy)
				if err != nil {
					return err
				}
				return printRecords(cmd, stats)
			}
			if clicky.Flags.ResolveFormat() == "ndjson" {
				print := recordPrinter(cmd)
				for row, err := range rows {
					if err != nil {
						return err
					}
					if err := print(row); err != nil {
						return err
					}
				}
				return nil
			}
			collected, err := query.CollectRows(rows)
			if err != nil {
				return err
			}
			if collected == nil {
				collected = []query.Row{}
			}
			return printRecords(cmd, collected)
		},
	}
	f := cmd.Flags()
	bindDataDir(f, &scope.DataDir)
	f.StringVar(&scope.Run, "run", "", "Run id (default: newest containing the kind)")
	f.StringSliceVar(&scope.Runs, "runs", nil, "Comma-separated run ids")
	f.StringVar(&since, "since", "", "Run-start cutoff and default row-time start: duration or RFC3339")
	f.StringVar(&from, "from", "", "Inclusive row-time start (RFC3339); overrides --since's row window")
	f.StringVar(&until, "until", "", "Exclusive row-time end (RFC3339)")
	f.Int64Var(&q.AfterSeq, "after-seq", 0, "Read rows after this sequence")
	f.Int64Var(&q.ToSeq, "to-seq", 0, "Read through this sequence (0: end)")
	f.StringArrayVar(&q.Filters, "filter", nil, "Column selection col=expr; repeatable, using profile filter grammar")
	f.StringVar(&q.Where, "where", "", "CEL row predicate, e.g. row.cpuPercent > 10")
	f.StringVarP(&q.Search, "query", "q", "", "Profile text search")
	f.IntVar(&q.Limit, "limit", 0, "Maximum matching rows (default: 100; aggregates: all)")
	f.StringVar(&q.Cursor, "cursor", "", "Continue from a previous page cursor")
	f.BoolVar(&q.All, "all", false, "Read every matching row")
	f.BoolVar(&follow, "follow", false, "Follow a single run through stream seal")
	f.StringSliceVar(&cols, "stats", nil, "Numeric columns to aggregate, comma-separated")
	f.StringVar(&groupBy, "group-by", "", "Group aggregates by this column; alone counts rows")
	f.StringVar(&view, "view", "", "Named ResultView to run over the selected seq window")
	return cmd
}

func recordValues(cmd *cobra.Command) url.Values {
	values := url.Values{}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name == dataDirFlag {
			return
		}
		if slice, ok := flag.Value.(pflag.SliceValue); ok {
			values[flag.Name] = slice.GetSlice()
		} else {
			values.Set(flag.Name, flag.Value.String())
		}
	})
	return values
}

func printRecords(cmd *cobra.Command, value any) error {
	output, err := clicky.Format(value)
	if err != nil {
		return err
	}
	if output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), output)
	return err
}

func recordPrinter(cmd *cobra.Command) func(query.Row) error {
	return func(row query.Row) error {
		if err := printRecords(cmd, row); err != nil {
			return err
		}
		if flush, ok := cmd.OutOrStdout().(interface{ Flush() error }); ok {
			return flush.Flush()
		}
		return nil
	}
}
