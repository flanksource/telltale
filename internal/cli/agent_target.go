package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// withAgentTarget is a capture's params with target.agent set to the id of
// the agent it runs on, when it runs on one: --agent may be a short name, or
// absent when only one agent offers the kind. A selector or a pid is left to
// the run.
func withAgentTarget(ctx context.Context, registration traces.Registration, raw json.RawMessage) (json.RawMessage, error) {
	params := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("trace %s params: %w", registration.Name, err)
		}
	}
	selection, _ := params["target"].(map[string]any)
	if selection == nil {
		selection = map[string]any{}
	}
	for _, other := range []string{"selector", "pid"} {
		if _, set := selection[other]; set {
			return raw, nil
		}
	}
	seam, _ := selection["seam"].(string)
	query, _ := selection["agent"].(string)
	if seam == "" && query == "" && registration.DefaultSeam != target.Agent {
		return raw, nil
	}
	if seam != "" && seam != string(target.Agent) {
		return raw, nil
	}
	agent, err := agentclient.Default.Lookup(ctx, query, agentclient.Want{Probe: registration.Name})
	if err != nil {
		return nil, err
	}
	selection["agent"] = agent.Hello.AgentID
	params["target"] = selection
	return json.Marshal(params)
}
