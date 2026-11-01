package definition

import (
	"errors"
	"fmt"

	"github.com/flanksource/telltale/pkg/tracing/target"
)

// Entry is one capture of a definition: a typed list's element.
type Entry interface {
	// EntryName is the name the entry sets, empty for its default.
	EntryName() string
	// options are the kind options and the checkpoint params, nil for none,
	// the entry compiles to at where it was found; the target is added by
	// the compiler.
	options(at Located) (options any, checkpoint any, err error)
}

// agentEntry is an entry that may read through a connected agent.
type agentEntry interface {
	agentRef() *AgentRef
}

// connectionEntry is an entry that reads a stored connection. Its slot is
// the field a default connection fills, the connection type that default
// must have, and whether the entry reads without one.
type connectionEntry interface {
	connection(at Located) connectionSlot
}

type connectionSlot struct {
	field *string
	// connType is the connection type a default must have.
	connType string
	// optional entries read without a connection, such as a query given a URL.
	optional bool
}

// Named is the optional name every entry carries; empty takes its default,
// <definition>-<section>[-<group>]-<type>[-<n>].
type Named struct {
	Name string `json:"name,omitempty" clicky:"title=Name,desc=Names the capture's session; empty takes <definition>-<section>-<group>-<type>-<n>"`
}

func (n *Named) EntryName() string { return n.Name }

func (n *Named) setEntryName(name string) { n.Name = name }

// AgentRef points a go or jvm entry at a connected telltale or Java agent
// instead of the execution's process.
type AgentRef struct {
	ID       string            `json:"id,omitempty" clicky:"title=Agent ID"`
	Selector map[string]string `json:"selector,omitempty" clicky:"title=Selector,desc=Agent resource attributes to match"`
	Wait     bool              `json:"wait,omitempty" clicky:"title=Wait,desc=Wait for a matching agent to connect"`
}

// Agent is embedded by the entries that may read through an agent.
type Agent struct {
	Agent *AgentRef `json:"agent,omitempty" clicky:"title=Agent,desc=Read through a connected agent instead of the execution's process"`
}

func (a *Agent) agentRef() *AgentRef { return a.Agent }

// aim is the target at's entry is armed at: its group's seam, or an agent
// it names; an instrument entry alone may change the target's runtime.
func aim(at Located) (target.Target, error) {
	aimed := target.Target{Seam: at.Seam, AllowMutation: at.Section == SectionInstrument}
	carrier, ok := at.Entry.(agentEntry)
	if !ok || carrier.agentRef() == nil {
		return aimed, nil
	}
	ref := carrier.agentRef()
	if at.Seam != target.Process && at.Seam != target.Agent {
		return target.Target{}, fmt.Errorf("agent: %s captures are aimed at the %s seam, not an agent", at.Group, at.Seam)
	}
	if (ref.ID == "") == (len(ref.Selector) == 0) {
		return target.Target{}, errors.New("agent: name exactly one of id or selector")
	}
	aimed.Seam, aimed.Agent, aimed.Selector, aimed.Wait = target.Agent, ref.ID, ref.Selector, ref.Wait
	return aimed, nil
}
