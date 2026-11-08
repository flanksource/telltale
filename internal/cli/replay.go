package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/internal/traceui"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/spf13/cobra"
)

func newTraceReplayCommand() *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use: "replay <run-id> [run-id...]", Short: "Replay recorded captures without rerunning their commands", Args: cobra.MinimumNArgs(1),
		Long:    "Read the saved captures of one or more trace/run IDs. Records are merged in sequence or timestamp order. --ui opens a local viewer with a run selector; --tui opens the interactive terminal. Replay never executes recorded commands.",
		Example: "  telltale trace replay buildhost20261107T123000Z\n  telltale trace replay <run-id> <other-run-id> --ui\n  telltale trace replay <run-id> --format ndjson --sort timestamp",
		RunE: func(cmd *cobra.Command, ids []string) (err error) {
			follow, tui, ui, order, err := followOptions(cmd)
			if err != nil {
				return err
			}
			if tui && ui {
				return fmt.Errorf("replay: --ui and --tui are mutually exclusive")
			}
			explicitFollow, err := cmd.Flags().GetBool("follow")
			if err != nil {
				return err
			}
			if explicitFollow && tui {
				return fmt.Errorf("replay: --follow cannot combine with --tui")
			}
			if tui && clicky.Flags.ResolveFormat() == "ndjson" {
				return fmt.Errorf("--tui cannot combine with ndjson")
			}
			format := clicky.Flags.ResolveFormat()
			if format != "ndjson" && format != "pretty" && format != "ansi" {
				return fmt.Errorf("replay supports ndjson, ansi or pretty output")
			}
			selected, err := read.ResolveRuns(read.Scope{DataDir: dataDir, Runs: ids})
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, selected.Close()) }()
			if ui {
				viewer, err := openRunViewer(cmd, server.ViewerOptions{DataDir: dataDir, RunIDs: ids})
				if err != nil {
					return err
				}
				return waitViewer(viewer)
			}
			if tui {
				final, err := tea.NewProgram(traceui.New(cmd.Context(), traceui.Options{Refs: selected.Refs, Runs: selected.Runs, Sort: order, Replay: true}), tea.WithContext(cmd.Context())).Run()
				if model, ok := final.(traceui.Model); ok {
					err = errors.Join(err, model.Err())
				}
				return err
			}
			if follow {
				return read.Feed(cmd.Context(), selected.Refs, read.Query{}, func(record read.Record) error { return printTraceRecord(cmd, record) })
			}
			return replayRecords(cmd, selected.Refs, order)
		},
	}
	bindDataDir(cmd.Flags(), &dataDir)
	cmd.Flags().Bool("follow", false, "Follow selected captures until their streams seal")
	cmd.Flags().Bool("ui", false, "Open a local web viewer for the selected runs until Ctrl+C")
	cmd.Flags().Bool("tui", false, "Replay records in an interactive terminal")
	cmd.Flags().String("sort", "seq", "Record order: seq or timestamp")
	return cmd
}

func replayRecords(cmd *cobra.Command, refs []read.Ref, order string) error {
	query := read.FeedQuery{Sort: order}
	for {
		page, err := read.FeedPage(cmd.Context(), refs, query)
		if err != nil {
			return err
		}
		for _, record := range page.Rows {
			if err := printTraceRecord(cmd, record); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		query.Cursor = page.NextCursor
	}
}

func printTraceRecord(cmd *cobra.Command, record read.Record) error {
	if clicky.Flags.ResolveFormat() == "ndjson" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
	}
	output, err := clicky.Format(record.Pretty(), clicky.FormatOptions{Format: "pretty"})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), output)
	return err
}
