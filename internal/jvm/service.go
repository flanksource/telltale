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
	if request.Agent == "" || request.Operation == "" {
		return nil, fmt.Errorf("agent and operation are required")
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
	agent := agentclient.Default.ByID(request.Agent)
	if agent == nil {
		if err := agentclient.Default.Discover(ctx, ""); err != nil {
			return nil, err
		}
		agent = agentclient.Default.ByID(request.Agent)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q is not connected", request.Agent)
	}
	if agent.Hello.Runtime.Lang != "java" {
		return nil, fmt.Errorf("agent %q is not a Java runtime", request.Agent)
	}
	return agent.Call(ctx, agentclient.CallRequest{Op: request.Operation, Params: request.Params, AllowMutation: request.AllowMutation})
}
