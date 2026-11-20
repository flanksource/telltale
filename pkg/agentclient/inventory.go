package agentclient

import (
	"context"
	"fmt"

	"github.com/flanksource/telltale/sdk/protocol"
)

// Info describes a connected agent or a known target without inventing a handshake.
type Info struct {
	*protocol.Hello
	AgentID   string            `json:"agentId"`
	Resource  map[string]string `json:"resource"`
	Connected bool              `json:"connected"`
	Group     string            `json:"group"`
	Known     *Known            `json:"known,omitempty"`
}

func ConnectedInfo(hello protocol.Hello) Info {
	return Info{Hello: &hello, AgentID: hello.AgentID, Resource: hello.Resource, Connected: true, Group: inventoryGroup(hello.Resource)}
}

func (r *Registry) Inventory(ctx context.Context) ([]Info, error) {
	if err := r.Discover(ctx, ""); err != nil {
		return nil, err
	}
	candidates, err := r.candidates(ctx, Want{})
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.id == "" || seen[candidate.id] {
			return nil, fmt.Errorf("agent inventory has an empty or duplicate id %q", candidate.id)
		}
		seen[candidate.id] = true
		if candidate.agent != nil {
			infos = append(infos, ConnectedInfo(candidate.agent.Hello))
			continue
		}
		if candidate.known == nil {
			return nil, fmt.Errorf("agent %q has no connection or known metadata", candidate.id)
		}
		infos = append(infos, Info{AgentID: candidate.id, Resource: candidate.known.Resource, Known: candidate.known, Group: inventoryGroup(candidate.known.Resource)})
	}
	return infos, nil
}

func inventoryGroup(resource map[string]string) string {
	if namespace := resource["k8s.namespace.name"]; namespace != "" {
		return "namespace/" + namespace
	}
	if provider := resource["cloud.provider"]; provider != "" {
		return "cloud/" + provider + "/" + resource["cloud.region"]
	}
	if host := resource["host.name"]; host != "" {
		return "host/" + host
	}
	return "other"
}

type Groups map[string][]Info

func GroupInventory(infos []Info) Groups {
	groups := Groups{}
	for _, info := range infos {
		groups[info.Group] = append(groups[info.Group], info)
	}
	return groups
}
