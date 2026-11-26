package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/prompt"
	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

func (p paramFlags) promptRequired(cmd *cobra.Command, defaultSeam target.Seam) error {
	ctx := cmd.Context()
	if _, remote := rpc.RequestFromContext(ctx); remote {
		return nil
	}
	if !prompt.PreferSink(ctx) && !(term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))) {
		return nil
	}
	values, err := p.values()
	if err != nil {
		return err
	}
	schema := *p.schema
	if schema.Properties["connection"] != nil && connectionSeam(cmd, values, defaultSeam) && !slices.Contains(schema.Required, "connection") {
		schema.Required = append(slices.Clone(schema.Required), "connection")
	}
	missing, err := missingRequiredParams(&schema, values, nil)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	form := rpc.OpenAPISchema{Type: "object", Required: missing, Properties: map[string]*rpc.OpenAPISchema{}}
	var fields []paramFlag
	for _, name := range missing {
		index := slices.IndexFunc(p.bound, func(param paramFlag) bool { return param.flag.Name == name })
		if index == -1 {
			return fmt.Errorf("--%s is required", name)
		}
		fields = append(fields, p.bound[index])
		form.Properties[name] = p.bound[index].schema
	}
	raw, err := json.Marshal(form)
	if err != nil {
		return fmt.Errorf("encode required trace params: %w", err)
	}
	answers, err := clicky.PromptForm(ctx, clicky.PromptFormOptions{Title: cmd.CommandPath(), Schema: raw, Lookup: lookupRunConnections})
	if err != nil {
		return fmt.Errorf("%s: %w", cmd.CommandPath(), err)
	}
	for _, param := range fields {
		if err := param.setAnswer(cmd.Flags(), answers[param.flag.Name]); err != nil {
			return err
		}
	}
	return nil
}

func connectionSeam(cmd *cobra.Command, values map[string]any, defaultSeam target.Seam) bool {
	selection, _ := values["target"].(map[string]any)
	if seam, _ := selection["seam"].(string); seam != "" {
		return seam == string(target.Connection)
	}
	pid := cmd.Flags().Lookup("pid")
	return defaultSeam == target.Connection && selection["agent"] == nil && selection["selector"] == nil &&
		(pid == nil || pid.Value.String() == "0")
}

func (p paramFlag) setAnswer(flags *pflag.FlagSet, answer any) error {
	if answer == nil {
		return fmt.Errorf("--%s is required", p.flag.Name)
	}
	if value, ok := p.flag.Value.(pflag.SliceValue); ok {
		var items []string
		switch answer := answer.(type) {
		case []any:
			for _, item := range answer {
				items = append(items, fmt.Sprint(item))
			}
		case map[string]any:
			for _, key := range slices.Sorted(maps.Keys(answer)) {
				items = append(items, key+"="+fmt.Sprint(answer[key]))
			}
		default:
			return fmt.Errorf("--%s: unexpected form answer %T", p.flag.Name, answer)
		}
		if err := value.Replace(items); err != nil {
			return fmt.Errorf("--%s: %w", p.flag.Name, err)
		}
		p.flag.Changed = true
		return nil
	}
	if text, ok := answer.(string); ok && p.schema.Type == "string" {
		return flags.Set(p.flag.Name, text)
	}
	raw, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("--%s: %w", p.flag.Name, err)
	}
	return flags.Set(p.flag.Name, string(raw))
}
