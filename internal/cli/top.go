package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/topui"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// topFlags are what `telltale top` reads and how it shows it.
type topFlags struct {
	params   groups.Params
	every    time.Duration
	sort     string
	groupBy  string
	reverse  bool
	record   bool
	once     bool
	duration time.Duration
	workDir  string
}

func newTopCommand() *cobra.Command {
	flags := topFlags{every: groups.DefaultInterval, sort: groups.SortCPU, groupBy: string(groups.GroupTree), workDir: "."}
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show the CPU, memory, disk, sockets and network of each tool's processes: agents, tests, browsers, builds, services",
		Long: "Sorts every process on the host into the tool that owns it — claude, codex, cmux, gavel, go test, vitest, agent-browser, chrome, " +
			"brew services and more — as a tree of groups, each with its checkout, agent session, cmux workspace and what started it, " +
			"and shows what each group and those under it use, full screen. The processes no tool holds are kept under other (the user's) " +
			"and system (root's and the role accounts'), by program.\n\n" +
			"Keys: ↑↓ move, ←→ fold, enter lists the selected group's processes, s sorts, S reverses, / filters, o shows other processes, " +
			"b cycles grouping, n reads network bytes, q quits.\n\n" +
			"Nothing is kept unless --record records a run into .telltale/traces, which `telltale trace list` and `telltale serve` show. " +
			"Rules come from the built-in catalog and .telltale/groups.yaml (or --catalog).",
		Example: "  telltale top\n  telltale top --groups claude --sort mem\n  telltale top --record --duration 10m --net\n  telltale top --once --format json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return flags.run(cmd)
		},
	}
	cmd.Flags().DurationVar(&flags.every, "every", flags.every, "How often the groups are read (at least 1s)")
	cmd.Flags().StringVar(&flags.sort, "sort", flags.sort, fmt.Sprintf("Sort siblings by, one of %v", groups.Sorts))
	cmd.Flags().StringVar(&flags.groupBy, "group-by", flags.groupBy, fmt.Sprintf("Group by, one of %v", groups.Groupings))
	cmd.Flags().BoolVar(&flags.reverse, "reverse", false, "Sort lowest first, or the name Z first")
	cmd.Flags().BoolVar(&flags.params.All, "all", false, "Also show the processes no tool's group holds, by scope and program")
	cmd.Flags().StringSliceVar(&flags.params.Groups, "groups", nil, "Show only these groups and those under them, e.g. claude,vitest")
	cmd.Flags().BoolVar(&flags.params.Net, "net", false, "Read each process's network bytes with nettop (darwin; nettop costs over a core)")
	cmd.Flags().StringVar(&flags.params.Catalog, "catalog", "", "A groups.yaml applied over the built-in rules (default .telltale/groups.yaml when present)")
	cmd.Flags().BoolVar(&flags.record, "record", false, "Record the groups into a run in .telltale/traces")
	cmd.Flags().BoolVar(&flags.once, "once", false, "Print one frame, taken once CPU can be rated, and exit")
	cmd.Flags().DurationVar(&flags.duration, "duration", 0, "Stop after this long (0 runs until q or Ctrl+C)")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", flags.workDir, "Directory holding .telltale/traces and .telltale/groups.yaml")
	return cmd
}

func (f topFlags) order() groups.Order { return groups.Order{By: f.sort, Reverse: f.reverse} }

func (f topFlags) run(cmd *cobra.Command) error {
	if err := groups.GroupBy(f.groupBy).Validate(); err != nil {
		return err
	}
	if f.record && f.once {
		return errors.New("--once prints one frame without recording: drop --record or --once")
	}
	if _, err := (groups.Frame{}).Sorted(f.order()); err != nil {
		return err
	}
	f.params.Interval = f.every.String()
	if f.params.Catalog == "" {
		var err error
		if f.params.Catalog, err = groups.CatalogFile(f.workDir); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.record {
		return f.recordRun(ctx)
	}
	if f.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.duration)
		defer cancel()
	}
	sampler := process.NewSampler()
	open := func(toggles topui.Toggles) (*groups.Top, error) {
		params := f.params
		params.All, params.Net = toggles.All, toggles.Net
		options, err := params.Options()
		if err != nil {
			return nil, err
		}
		return groups.NewTop(ctx, sampler, options)
	}
	toggles := topui.Toggles{All: f.params.All, Net: f.params.Net}
	top, err := open(toggles)
	if err != nil {
		return err
	}
	if f.once || !interactive() {
		defer top.Close()
		return f.printOnce(ctx, top)
	}
	return f.show(ctx, topui.Options{
		Source: topui.Live{Top: top}, Toggles: toggles, Every: f.every, Order: f.order(), Title: "live",
		Reopen: func(toggles topui.Toggles) (topui.Source, error) {
			top, err := open(toggles)
			if err != nil {
				return nil, err
			}
			return topui.Live{Top: top}, nil
		},
	})
}

// interactive reports whether the view can take over the terminal: one it
// reads keys from and draws on, in the pretty format.
func interactive() bool {
	format := clicky.Flags.Format
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) && (format == "" || format == "pretty")
}

// printOnce prints the second frame once the Top is ready, the first whose
// readings are all rated.
func (f topFlags) printOnce(ctx context.Context, top *groups.Top) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-top.Ready():
	}
	if _, err := top.Tick(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.every):
	}
	frame, err := top.Tick(ctx)
	if err != nil {
		return err
	}
	projected, err := frame.Grouped(groups.GroupBy(f.groupBy))
	if err != nil {
		return err
	}
	sorted, err := projected.Sorted(f.order())
	if err != nil {
		return err
	}
	clicky.MustPrint(sorted)
	return nil
}

// recordRun records a host run of process_groups, showing the frames its
// store commits until it is stopped or its duration passes, then what it
// recorded.
func (f topFlags) recordRun(ctx context.Context) error {
	raw, err := json.Marshal(f.params)
	if err != nil {
		return fmt.Errorf("encode %s params: %w", groups.Kind, err)
	}
	connections, err := telltale.OpenConnections()
	if err != nil {
		return err
	}
	run, err := telltale.Start(ctx, telltale.StartOptions{
		WorkDir: f.workDir, Duration: f.duration, Connections: connections,
		Traces: []telltale.TraceSpec{{Kind: groups.Kind, Params: raw}},
	})
	if err != nil {
		return err
	}
	if interactive() {
		follower, err := groups.NewFollower(run.Runtime(), run.Info().Captures[0].Ref)
		if err != nil {
			return errors.Join(err, run.Stop())
		}
		err = f.show(ctx, topui.Options{
			Source: topui.Recorded{Follower: follower}, Toggles: topui.Toggles{All: f.params.All, Net: f.params.Net},
			Every: f.every, Order: f.order(), Title: "recording into " + run.Dir(), Done: run.Done(),
		})
		// A read the run's end cut short, its store closing, is the end, not a failure.
		if stopErr := run.Stop(); err == nil && !ended(run.Done()) {
			err = stopErr
		}
		if err != nil && !ended(run.Done()) {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Recording process groups into %s; Ctrl+C stops\n", run.Dir()) // a lost notice loses nothing recorded
	}
	result, err := run.Wait()
	if err != nil {
		return err
	}
	clicky.MustPrint(result)
	return nil
}

func ended(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// show runs the full-screen view until q, Ctrl+C, ctx ending or its source
// ending, and returns why it failed, if it did.
func (f topFlags) show(ctx context.Context, options topui.Options) error {
	options.GroupBy = groups.GroupBy(f.groupBy)
	model := topui.New(ctx, options)
	final, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if last, ok := final.(topui.Model); ok {
		last.Close()
		if err == nil {
			err = last.Err()
		}
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
