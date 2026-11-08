package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/spf13/cobra"
)

type kindExplanation struct {
	tracing.KindInfo
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Seams       []target.Seam `json:"seams"`
	DefaultSeam target.Seam   `json:"defaultSeam"`
}

func (e kindExplanation) Pretty() api.Text {
	t := clicky.Text(e.Name, "font-bold").Append(" — " + e.Summary).NewLine()
	if e.Description != "" {
		t = t.Append(e.Description).NewLine()
	}
	seams := make([]string, 0, len(e.Seams))
	for _, seam := range e.Seams {
		seams = append(seams, string(seam))
	}
	t = t.Append("Targets: " + strings.Join(seams, ", ") + " (default: " + string(e.DefaultSeam) + ")").NewLine()
	data, err := json.MarshalIndent(e.Params, "", "  ")
	if err != nil {
		panic(fmt.Errorf("marshal %s parameter schema: %w", e.Name, err))
	}
	t = t.Append("Parameters", "font-bold").NewLine().Add(clicky.CodeBlock("application/json", string(data))).NewLine()
	t = t.Append("Record schema", "font-bold").NewLine()
	for _, column := range e.Columns {
		t = t.Append(fmt.Sprintf("%s  %s  %s", column.Name, column.Type, column.Label)).NewLine()
	}
	return t
}

func newExplainCommand() *cobra.Command {
	return &cobra.Command{
		Use: "explain [kind]", Short: "Describe trace kinds and their schemas", Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
			var names []string
			for _, registration := range traces.Registrations() {
				if strings.HasPrefix(registration.Name, prefix) {
					names = append(names, registration.Name)
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				var list []struct {
					Name    string `json:"name"`
					Summary string `json:"summary"`
				}
				for _, registration := range traces.Registrations() {
					list = append(list, struct {
						Name    string `json:"name"`
						Summary string `json:"summary"`
					}{registration.Name, registration.Summary})
				}
				return printRecords(cmd, list)
			}
			kinds, err := traces.Kinds()
			if err != nil {
				return err
			}
			if _, err := traces.Lookup(kinds, args[0]); err != nil {
				return err
			}
			registration, err := traces.Registered(args[0])
			if err != nil {
				return err
			}
			list, err := kinds.List()
			if err != nil {
				return err
			}
			for _, info := range list {
				if info.Name == args[0] {
					return printRecords(cmd, kindExplanation{info, registration.Summary, registration.Description, registration.Seams, registration.DefaultSeam})
				}
			}
			return fmt.Errorf("trace kind %q is missing from catalog", args[0])
		},
	}
}
