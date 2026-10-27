package cli

import (
	"encoding/json"
	"github.com/flanksource/telltale/internal/rules"
	"github.com/spf13/cobra"
	"net/url"
)

func newRulesCommand() *cobra.Command {
	command := &cobra.Command{Use: "rules", Short: "Validate, preview and inspect CEL resource rules"}
	var file, workDir, server string
	command.PersistentFlags().StringVar(&file, "rules", "", "Rules YAML file")
	command.PersistentFlags().StringVar(&workDir, "work-dir", ".", "Directory containing the process group catalog")
	command.PersistentFlags().StringVar(&server, "server", "http://localhost:8080", "Running Telltale server for status and events")
	validate := &cobra.Command{Use: "validate", Short: "Compile and type-check every rule and target expression", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		program, err := rules.Load(file)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"valid": true, "revision": program.Revision, "rules": len(program.Config.Rules)})
	}}
	preview := &cobra.Command{Use: "preview", Short: "Preview resource conditions and exact hook targets without enforcing", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := rules.PreviewFile(cmd.Context(), file, workDir)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	for _, child := range []*cobra.Command{validate, preview} {
		command.AddCommand(child)
	}
	for _, name := range []string{"status", "events"} {
		name := name
		child := &cobra.Command{Use: name, Short: "Read rules " + name + " from the running server", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			query := url.Values{}
			if name == "events" {
				for _, key := range []string{"rule", "group", "incident", "since", "limit"} {
					query.Set(key, cmd.Flag(key).Value.String())
				}
			}
			return rules.Fetch(cmd.Context(), rules.FetchOptions{Server: server, Operation: name, Query: query, Output: cmd.OutOrStdout()})
		}}
		if name == "events" {
			child.Flags().String("rule", "", "Filter by rule name")
			child.Flags().String("group", "", "Filter by stable group key")
			child.Flags().String("incident", "", "Filter by incident ID")
			child.Flags().String("since", "", "Events since an RFC3339 timestamp")
			child.Flags().Int("limit", 100, "Maximum events (1..10000)")
		}
		command.AddCommand(child)
	}
	return command
}
