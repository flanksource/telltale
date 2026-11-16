package agentclient

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Known is an agent a resolver can connect but that is not connected yet,
// such as a deployed target, with the short names it answers to.
type Known struct {
	ID string
	// Runtime is the agent's language, when known before it connects.
	Runtime string
	Names   []string
}

// Lister lists the agents its resolver can connect.
type Lister func(ctx context.Context) ([]Known, error)

// SetLister makes lister name the agents the resolver of prefix connects, so
// a lookup can find them by a short name before they are connected.
func (r *Registry) SetLister(prefix string, lister Lister) {
	if prefix == "" || lister == nil {
		panic("agentclient: SetLister needs a prefix and a lister")
	}
	r.listMu.Lock()
	defer r.listMu.Unlock()
	if r.listers == nil {
		r.listers = map[string]Lister{}
	}
	r.listers[prefix] = lister
}

// Want narrows a lookup to the agents that can serve it.
type Want struct {
	// Probe is a probe the agent must offer.
	Probe string
	// Runtime is the agent's language, such as java.
	Runtime string
}

func (w Want) String() string {
	switch {
	case w.Probe != "":
		return "agent offering " + w.Probe
	case w.Runtime != "":
		return w.Runtime + " agent"
	}
	return "agent"
}

// candidate is a connected or known agent and the names a query matches.
type candidate struct {
	id, label string
	names     []string
	agent     *Agent
}

// Lookup is the one agent query names: its id, or a short name (its service,
// pod or deployer, the last part of its id, its pid, or a unique prefix of
// one), among the connected agents and those a resolver can connect. An empty
// query names the only agent there is. A known agent is connected first.
func (r *Registry) Lookup(ctx context.Context, query string, want Want) (*Agent, error) {
	if query != "" {
		if agent := r.ByID(query); agent != nil {
			return agent, nil
		}
		if claimed, err := r.Resolve(ctx, query); claimed {
			if err != nil {
				return nil, err
			}
			return r.ByID(query), nil
		}
	}
	if err := r.Discover(ctx, ""); err != nil {
		return nil, err
	}
	candidates, err := r.candidates(ctx, want)
	if err != nil {
		return nil, err
	}
	matches := matching(candidates, query)
	switch {
	case len(matches) == 1 && matches[0].agent != nil:
		return matches[0].agent, nil
	case len(matches) == 1:
		if _, err := r.Resolve(ctx, matches[0].id); err != nil {
			return nil, err
		}
		if agent := r.ByID(matches[0].id); agent != nil {
			return agent, nil
		}
		return nil, fmt.Errorf("agent %q: no resolver connects it", matches[0].id)
	case len(candidates) == 0 && query == "":
		return nil, fmt.Errorf("no %s is connected or deployed: attach one with telltale agents install java, or deploy one with telltale jvm deploy", want)
	case len(matches) == 0:
		return nil, fmt.Errorf("no %s matches %q; agents: %s", want, query, labels(candidates))
	case query == "":
		return nil, fmt.Errorf("%d %ss are connected: %s; name one with --agent", len(matches), want, labels(matches))
	}
	return nil, fmt.Errorf("agent %q matches %d agents: %s; name one by its id", query, len(matches), labels(matches))
}

func (r *Registry) candidates(ctx context.Context, want Want) ([]candidate, error) {
	var result []candidate
	connected := map[string]bool{}
	for _, agent := range r.Match(nil) {
		connected[agent.Hello.AgentID] = true
		if (want.Probe != "" && !agent.HasProbe(want.Probe)) || (want.Runtime != "" && agent.Hello.Runtime.Lang != want.Runtime) {
			continue
		}
		hello := agent.Hello
		names := []string{hello.Service, lastPart(hello.AgentID), hello.Resource["k8s.pod.name"], hello.Resource["telltale.deployer"], hello.Resource["process.pid"]}
		result = append(result, candidate{id: hello.AgentID, label: hello.Service, names: names, agent: agent})
	}
	r.listMu.Lock()
	listers := make([]Lister, 0, len(r.listers))
	for _, lister := range r.listers {
		listers = append(listers, lister)
	}
	r.listMu.Unlock()
	for _, lister := range listers {
		known, err := lister(ctx)
		if err != nil {
			return nil, err
		}
		for _, agent := range known {
			if connected[agent.ID] || (want.Runtime != "" && agent.Runtime != "" && agent.Runtime != want.Runtime) {
				continue
			}
			label := "not connected"
			if len(agent.Names) > 0 {
				label = agent.Names[0]
			}
			result = append(result, candidate{id: agent.ID, label: label, names: append([]string{lastPart(agent.ID)}, agent.Names...)})
		}
	}
	slices.SortFunc(result, func(a, b candidate) int { return strings.Compare(a.id, b.id) })
	return result, nil
}

// matching are the candidates query names exactly, else those it prefixes a
// name of; every candidate for an empty query.
func matching(candidates []candidate, query string) []candidate {
	if query == "" {
		return candidates
	}
	for _, exact := range []bool{true, false} {
		var found []candidate
		for _, c := range candidates {
			if slices.ContainsFunc(append([]string{c.id}, c.names...), func(name string) bool {
				return name != "" && (name == query || (!exact && strings.HasPrefix(name, query)))
			}) {
				found = append(found, c)
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

func lastPart(id string) string { return id[strings.LastIndex(id, "/")+1:] }

func labels(candidates []candidate) string {
	parts := make([]string, len(candidates))
	for i, c := range candidates {
		parts[i] = c.id + " (" + c.label + ")"
	}
	return strings.Join(parts, ", ")
}
