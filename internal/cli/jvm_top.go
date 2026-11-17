package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/jvm/top"
	"github.com/flanksource/telltale/internal/jvm/top/tui"
	"github.com/flanksource/telltale/internal/shutdown"
	"github.com/spf13/cobra"
)

func newJVMTopCommand() *cobra.Command {
	var agent, server string
	every := 2 * time.Second
	var once bool
	cmd := &cobra.Command{Use: "top", Short: "Monitor one JVM's CPU, memory, GC and busiest threads", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		source := &top.Source{Agent: agent, Server: server}
		if every < time.Second {
			return fmt.Errorf("--every must be at least 1s")
		}
		_, remote := rpc.RequestFromContext(cmd.Context())
		if remote && source.Server != "" {
			return fmt.Errorf("--server is only supported by the local CLI")
		}
		if remote || once || !interactive() {
			frame, err := source.Next(cmd.Context())
			if err != nil {
				return err
			}
			if remote {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(frame)
			}
			return printRecords(cmd, frame)
		}
		restore, err := shutdown.Terminal(cmd.Context())
		if err != nil {
			return err
		}
		defer restore()
		final, err := tea.NewProgram(tui.New(ctx, source, every), tea.WithContext(ctx)).Run()
		if err != nil {
			return err
		}
		if model, ok := final.(tui.Model); ok {
			return model.Err()
		}
		return nil
	}
	cmd.Flags().StringVar(&agent, "agent", "", agentFlagUsage)
	cmd.Flags().StringVar(&server, "server", "", "Telltale hub URL (uses TELLTALE_API_TOKEN)")
	cmd.Flags().DurationVar(&every, "every", every, "Refresh interval (at least 1s)")
	cmd.Flags().BoolVar(&once, "once", false, "Print one snapshot and exit")
	return cmd
}
