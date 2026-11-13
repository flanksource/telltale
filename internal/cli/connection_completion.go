package cli

import (
	"encoding/json"
	"github.com/flanksource/telltale"
	"github.com/spf13/cobra"
	"net/url"
	"slices"
	"strings"
)

func bindConnectionCompletions(cmd *cobra.Command, params paramFlags) error {
	for _, param := range params.bound {
		if param.path[len(param.path)-1] != "connection" {
			continue
		}
		raw, err := json.Marshal(param.schema.Extensions["x-clicky-lookup"])
		if err != nil {
			return err
		}
		var lookup struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &lookup); err != nil {
			return err
		}
		endpoint, err := url.Parse(lookup.URL)
		if err != nil {
			return err
		}
		types := strings.Split(endpoint.Query().Get("types"), ",")
		if err := cmd.RegisterFlagCompletionFunc(param.flag.Name, func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
			connections, err := telltale.OpenConnections()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			infos, err := telltale.ListConnections(connections, "")
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			choices := []string{}
			for _, info := range infos {
				if strings.HasPrefix(info.Reference, prefix) && (types[0] == "" || slices.Contains(types, info.Type)) {
					choices = append(choices, info.Reference+"\t"+info.Name+" ("+info.Type+")")
				}
			}
			return choices, cobra.ShellCompDirectiveNoFileComp
		}); err != nil {
			return err
		}
	}
	return nil
}
