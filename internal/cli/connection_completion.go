package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/flanksource/clicky/schemaform"
	"github.com/flanksource/telltale"
	"github.com/spf13/cobra"
)

func bindConnectionCompletions(cmd *cobra.Command, params paramFlags) error {
	for _, param := range params.bound {
		if param.path[len(param.path)-1] != "connection" {
			continue
		}
		const extension = "x-clicky-lookup"
		metadata := param.schema.Extensions[extension]
		if metadata == nil {
			metadata = schemaform.LookupSpec{URL: "/api/v1/connection", Filter: "connection", SearchParam: "__lookup_q"}
			extensions := map[string]any{}
			maps.Copy(extensions, param.schema.Extensions)
			extensions[extension] = metadata
			param.schema.Extensions = extensions
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		var lookup schemaform.LookupSpec
		if err := json.Unmarshal(raw, &lookup); err != nil {
			return err
		}
		types, err := connectionLookupTypes(lookup.URL)
		if err != nil {
			return err
		}
		if err := cmd.RegisterFlagCompletionFunc(param.flag.Name, func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
			infos, err := connectionChoices(types)
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			choices := []string{}
			for _, info := range infos {
				if strings.HasPrefix(info.Reference, prefix) {
					choices = append(choices, info.Reference+"\t"+connectionChoiceLabel(info))
				}
			}
			return choices, cobra.ShellCompDirectiveNoFileComp
		}); err != nil {
			return err
		}
	}
	return nil
}

func connectionLookupTypes(endpoint string) ([]string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if types := u.Query().Get("types"); types != "" {
		return strings.Split(types, ","), nil
	}
	return nil, nil
}

func connectionChoices(types []string) ([]telltale.ConnectionInfo, error) {
	connections, err := telltale.OpenConnections()
	if err != nil {
		return nil, err
	}
	infos, err := telltale.ListConnections(connections, "")
	if err != nil {
		return nil, err
	}
	if len(types) > 0 {
		infos = slices.DeleteFunc(infos, func(info telltale.ConnectionInfo) bool { return !slices.Contains(types, info.Type) })
	}
	return infos, nil
}

func connectionChoiceLabel(info telltale.ConnectionInfo) string {
	return info.Name + " (" + info.Type + ")"
}

func lookupRunConnections(_ context.Context, request schemaform.LookupRequest) (schemaform.LookupResult, error) {
	if request.Spec.Filter != "connection" {
		return schemaform.LookupResult{}, fmt.Errorf("unknown trace param lookup %q", request.Spec.Filter)
	}
	types, err := connectionLookupTypes(request.Spec.URL)
	if err != nil {
		return schemaform.LookupResult{}, err
	}
	infos, err := connectionChoices(types)
	if err != nil {
		return schemaform.LookupResult{}, err
	}
	if len(infos) == 0 {
		return schemaform.LookupResult{}, fmt.Errorf("no configured connections match %q; see telltale connection list", request.Spec.URL)
	}
	options := make([]schemaform.Option, 0, len(infos))
	for _, info := range infos {
		label := connectionChoiceLabel(info)
		if info.Namespace != "" {
			label = info.Namespace + "/" + label
		}
		options = append(options, schemaform.Option{Value: info.Reference, Raw: info.Reference, Label: label, Description: info.Reference})
	}
	return schemaform.LookupResult{Options: options, Total: len(options)}, nil
}
