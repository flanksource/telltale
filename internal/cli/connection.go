package cli

import (
	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale"
	"github.com/spf13/cobra"
)

func newConnectionCommand() *cobra.Command {
	connection := &cobra.Command{
		Use:   "connection",
		Short: "Show the connections traces can name: ~/.config/flanksource/connections, ~/.config/telltale/connections, then ./.connections",
	}
	var connectionType string
	list := &cobra.Command{
		Use:   "list",
		Short: "List the connections in effect, each from the last layer that defines it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			connections, err := telltale.OpenConnections()
			if err != nil {
				return err
			}
			infos, err := telltale.ListConnections(connections, connectionType)
			if err != nil {
				return err
			}
			clicky.MustPrint(infos)
			return nil
		},
	}
	list.Flags().StringVar(&connectionType, "type", "", "Keep connections of this type, e.g. sql_server or loki")
	get := &cobra.Command{
		Use:   "get <name | connection://[namespace/]name>",
		Short: "Show the connection a reference resolves to",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			connections, err := telltale.OpenConnections()
			if err != nil {
				return err
			}
			info, err := telltale.GetConnection(connections, args[0])
			if err != nil {
				return err
			}
			clicky.MustPrint(info)
			return nil
		},
	}
	connection.AddCommand(list, get)
	return connection
}
