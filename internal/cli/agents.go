package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/spf13/cobra"
)

func newAgentsCommand() *cobra.Command {
	agents := &cobra.Command{Use: "agents", Short: "Manage agents and their credentials"}
	var serverURL string
	list := &cobra.Command{
		Use: "list", Short: "List discovered and connected agents", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			infos, err := agentInfos(cmd.Context(), serverURL)
			if err != nil {
				return err
			}
			groups := make(map[string][]protocol.Hello)
			for _, info := range infos {
				key := "other"
				if namespace := info.Resource["k8s.namespace.name"]; namespace != "" {
					key = "namespace/" + namespace
				} else if provider := info.Resource["cloud.provider"]; provider != "" {
					key = "cloud/" + provider + "/" + info.Resource["cloud.region"]
				} else if host := info.Resource["host.name"]; host != "" {
					key = "host/" + host
				}
				groups[key] = append(groups[key], info)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(groups)
		},
	}
	get := &cobra.Command{
		Use: "get <id>", Short: "Show a discovered or connected agent", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := agentInfos(cmd.Context(), serverURL)
			if err != nil {
				return err
			}
			for _, info := range infos {
				if info.AgentID == args[0] {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
				}
			}
			return fmt.Errorf("agent %q not found", args[0])
		},
	}
	list.Flags().StringVar(&serverURL, "server", "", "Telltale server URL for connected agents")
	get.Flags().StringVar(&serverURL, "server", "", "Telltale server URL for connected agents")
	tokens := &cobra.Command{Use: "token", Short: "Manage hub credentials"}
	var workDir string
	var scope string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a hub token and print it once", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			minted, err := agentclient.NewTokenStore(workDir).Mint(args[0], apitoken.Scope(scope))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), minted.Secret.Value())
			return err
		},
	}
	create.Flags().StringVar(&workDir, "work-dir", ".", "Directory containing the hub state")
	create.Flags().StringVar(&scope, "scope", string(apitoken.ScopeAgent), "Token scope: agent or api")
	tokens.AddCommand(create)
	agents.AddCommand(list, get, tokens)
	return agents
}

func agentInfos(ctx context.Context, serverURL string) (infos []protocol.Hello, err error) {
	if serverURL != "" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/api/agents", nil)
		if err != nil {
			return nil, err
		}
		if token := os.Getenv("TELLTALE_API_TOKEN"); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("list agents: %w", err)
		}
		defer func() { err = errors.Join(err, response.Body.Close()) }()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list agents: HTTP %d", response.StatusCode)
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&infos); err != nil {
			return nil, fmt.Errorf("decode agents: %w", err)
		}
		return infos, nil
	}
	if err := agentclient.Default.Discover(ctx, ""); err != nil {
		return nil, err
	}
	infos = make([]protocol.Hello, 0)
	for _, agent := range agentclient.Default.Match(nil) {
		infos = append(infos, agent.Hello)
	}
	return infos, nil
}
