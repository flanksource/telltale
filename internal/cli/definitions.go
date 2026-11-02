package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/spf13/cobra"
)

// newDefinitionsCommand is `definitions list|get|validate|crd` over the trace
// definitions of the config dirs.
func newDefinitionsCommand() *cobra.Command {
	definitions := &cobra.Command{
		Use:   "definitions",
		Short: "Show and check trace definitions: .telltale/traces, ~/.config/telltale/traces, then the built-ins",
	}
	list := &cobra.Command{
		Use: "list", Short: "List the definitions in effect, each from the first layer that has it", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			defs, err := definition.OpenStore()
			if err != nil {
				return err
			}
			all, err := defs.List()
			if err != nil {
				return err
			}
			return printRecords(cmd, definition.Summaries(all))
		},
	}
	get := &cobra.Command{
		Use: "get <name>", Short: "Show a definition, its file and its resolved entries", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			defs, err := definition.OpenStore()
			if err != nil {
				return err
			}
			stored, err := defs.Get(args[0])
			if err != nil {
				return err
			}
			options, err := definitionOptions()
			if err != nil {
				return err
			}
			return printRecords(cmd, definition.Describe(stored, options))
		},
	}
	definitions.AddCommand(list, get, newValidateDefinitionCommand(), newCRDCommand())
	return definitions
}

func newValidateDefinitionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "validate <file>", Short: "Check a definition file statically and show its resolved entries", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stored, err := definition.LoadFile(args[0])
			if err != nil {
				return err
			}
			options, err := definitionOptions()
			if err != nil {
				return err
			}
			validation := definition.Check(stored.Definition, options)
			if err := printRecords(cmd, validation); err != nil {
				return err
			}
			if len(validation.Errors) > 0 {
				return fmt.Errorf("trace definition %s: %d error(s)", args[0], len(validation.Errors))
			}
			return nil
		},
	}
}

func newCRDCommand() *cobra.Command {
	return &cobra.Command{
		Use: "crd", Short: "Print the TraceDefinition CustomResourceDefinition", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			crd, err := definition.CRD()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(crd)
			return err
		},
	}
}

// definitionOptions resolve default connections in telltale's connections.
func definitionOptions() (definition.Options, error) {
	connections, err := telltale.OpenConnections()
	if err != nil {
		return definition.Options{}, err
	}
	return definition.Options{Connections: connections}, nil
}

// loadDefinition is the definition ref names: the file at ref when it is a
// path (it holds a separator or ends in .yaml), else the stored one so named.
func loadDefinition(ref string) (definition.Stored, error) {
	if strings.ContainsRune(ref, filepath.Separator) || strings.HasSuffix(ref, ".yaml") {
		return definition.LoadFile(ref)
	}
	defs, err := definition.OpenStore()
	if err != nil {
		return definition.Stored{}, err
	}
	return defs.Get(ref)
}

// runDefinition runs the definition ref names against options' command or
// pid, or as a host run with neither.
func runDefinition(cmd *cobra.Command, ref string, options telltale.StartOptions) error {
	stored, err := loadDefinition(ref)
	if err != nil {
		return err
	}
	return runTraced(cmd, options, func(ctx context.Context, options telltale.StartOptions) (*telltale.Run, error) {
		applied, err := definition.Apply(stored.Definition, options)
		if err != nil {
			return nil, err
		}
		return telltale.Start(ctx, applied)
	})
}
