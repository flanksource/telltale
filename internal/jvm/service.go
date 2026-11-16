package jvm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/flanksource/telltale/pkg/agentclient"
)

type Request struct {
	Agent         string          `json:"agent"`
	Operation     string          `json:"operation"`
	Params        json.RawMessage `json:"params"`
	AllowMutation bool            `json:"allowMutation"`
}

func Call(ctx context.Context, request Request) (json.RawMessage, error) {
	if request.Operation == "" {
		return nil, fmt.Errorf("an operation is required")
	}
	if len(request.Params) == 0 {
		request.Params = json.RawMessage(`{}`)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return nil, fmt.Errorf("operation parameters: %w", err)
	}
	if params == nil {
		return nil, fmt.Errorf("operation parameters must be an object")
	}
	// The agent may be named by a short name, or not at all when only one Java agent is there.
	agent, err := agentclient.Default.Lookup(ctx, request.Agent, agentclient.Want{Runtime: "java"})
	if err != nil {
		return nil, err
	}
	if agent.Hello.Runtime.Lang != "java" {
		return nil, fmt.Errorf("agent %q is not a Java runtime", agent.Hello.AgentID)
	}
	return agent.Call(ctx, agentclient.CallRequest{Op: request.Operation, Params: request.Params, AllowMutation: request.AllowMutation})
}
