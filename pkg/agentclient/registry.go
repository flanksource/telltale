package agentclient

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Default is the process-wide registry used by built-in tracing kinds.
var Default = NewRegistry()

// Registry tracks connected agents and publishes joins.
type Registry struct {
	Dir         string
	mu          sync.RWMutex
	discoveryMu sync.Mutex
	agents      map[string]*Agent
	subscribers map[chan *Agent]struct{}
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*Agent), subscribers: make(map[chan *Agent]struct{})}
}

// Add registers an authenticated agent.
func (r *Registry) Add(agent *Agent) error {
	if agent == nil || agent.Hello.AgentID == "" {
		return fmt.Errorf("agent registry: missing agent identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.add(agent)
}

func (r *Registry) add(agent *Agent) error {
	if _, exists := r.agents[agent.Hello.AgentID]; exists {
		return fmt.Errorf("agent registry: duplicate agent %q", agent.Hello.AgentID)
	}
	for subscriber := range r.subscribers {
		if len(subscriber) == cap(subscriber) {
			return fmt.Errorf("agent registry: join subscriber is full")
		}
	}
	r.agents[agent.Hello.AgentID] = agent
	for subscriber := range r.subscribers {
		subscriber <- agent
	}
	go r.removeOnDisconnect(agent)
	return nil
}

func (r *Registry) addHub(agent *Agent) error {
	if agent == nil || agent.Hello.AgentID == "" {
		return fmt.Errorf("hub agent missing identity")
	}
	id := agent.Hello.AgentID
	numeric := strings.TrimPrefix(strings.TrimPrefix(id, "+"), "-")
	if numeric != "" && strings.Trim(numeric, "0123456789") == "" {
		return fmt.Errorf("hub agent identity %q is numeric", id)
	}
	if strings.HasPrefix(id, "hub/") {
		return fmt.Errorf("hub agent identity %q uses reserved namespace", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents[id] != nil {
		return fmt.Errorf("hub agent identity %q collision", id)
	}
	agent.Hello.AgentID = "hub/" + id
	return r.add(agent)
}

func (r *Registry) removeOnDisconnect(agent *Agent) {
	<-agent.done
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents[agent.Hello.AgentID] == agent {
		delete(r.agents, agent.Hello.AgentID)
	}
}

// ByID returns a connected agent by identity.
func (r *Registry) ByID(id string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

// ByPID returns a connected agent for a process.
func (r *Registry) ByPID(pid int) *Agent {
	if pid <= 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, agent := range r.agents {
		if agent.localPID == pid {
			return agent
		}
	}
	return nil
}

// HasProbe reports whether the process has a connected agent advertising probe.
func (r *Registry) HasProbe(pid int, probe string) bool {
	agent := r.ByPID(pid)
	return agent != nil && agent.HasProbe(probe)
}

// HasProbe reports whether the agent advertises probe.
func (a *Agent) HasProbe(name string) bool {
	for _, probe := range a.Hello.Probes {
		if probe.Name == name {
			return true
		}
	}
	return false
}

// Discover connects newly discovered local agents.
func (r *Registry) Discover(ctx context.Context, dir string) error {
	r.discoveryMu.Lock()
	defer r.discoveryMu.Unlock()
	if dir == "" {
		dir = r.Dir
	}
	var endpoints []Endpoint
	var err error
	if dir == "" {
		endpoints, err = Discover()
	} else {
		endpoints, err = DiscoverIn(dir)
	}
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		if r.ByID(strconv.Itoa(endpoint.PID)) != nil {
			continue
		}
		agent, err := Dial(ctx, endpoint)
		if err != nil {
			return fmt.Errorf("discover pid %d: %w", endpoint.PID, err)
		}
		agent.localPID = endpoint.PID
		if err := r.Add(agent); err != nil {
			_ = agent.Close()
			return err
		}
	}
	return nil
}

// Match returns agents whose labels or resource match every predicate.
func (r *Registry) Match(selector map[string]string) []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*Agent
	for _, agent := range r.agents {
		matched := true
		for key, value := range selector {
			if agent.Hello.Resource[key] != value && agent.Hello.Labels[key] != value {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, agent)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Hello.AgentID < result[j].Hello.AgentID })
	return result
}

// Subscribe returns join notifications until unsubscribe is called.
func (r *Registry) Subscribe() (<-chan *Agent, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan *Agent, 256)
	r.subscribers[ch] = struct{}{}
	var once sync.Once
	return ch, func() { once.Do(func() { r.mu.Lock(); defer r.mu.Unlock(); delete(r.subscribers, ch); close(ch) }) }
}
