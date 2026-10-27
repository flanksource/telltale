package telltale

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/flanksource/telltale/sdk/protocol"
)

// Probe installs a capture and returns a function that releases it.
type Probe interface {
	Spec() protocol.ProbeSpec
	Arm(context.Context, json.RawMessage, Sink) (func() error, error)
}

// Installer installs a shared observation hook on the first session and removes it on the last.
type Installer interface {
	Install() (func() error, error)
}

// MutationProbe determines whether a particular set of parameters mutates the process.
type MutationProbe interface {
	Mutating(json.RawMessage) (bool, error)
}

// Checkpointer reads a probe on demand.
type Checkpointer interface {
	Checkpoint(context.Context, json.RawMessage, Sink) error
}

// Sink receives records and artifacts from a probe.
type Sink interface {
	TryEmit(any) bool
	Blob(string, string, []byte, any) error
	Report(any)
	Warn(string)
}

// Op performs a one-shot operation.
type Op interface {
	Spec() protocol.OpSpec
	Call(context.Context, json.RawMessage) (any, []protocol.Blob, error)
}

var catalog = struct {
	sync.RWMutex
	probes map[string]Probe
	ops    map[string]Op
}{probes: make(map[string]Probe), ops: make(map[string]Op)}

// RegisterProbe adds a probe, panicking on duplicate or empty names.
func RegisterProbe(probe Probe) {
	catalog.Lock()
	defer catalog.Unlock()
	spec, err := probeSpec(probe)
	if err != nil {
		panic(err)
	}
	name := spec.Name
	if name == "" || catalog.probes[name] != nil {
		panic("duplicate or empty probe: " + name)
	}
	catalog.probes[name] = probe
}

// RegisterOp adds an operation, panicking on duplicate or empty names.
func RegisterOp(op Op) {
	catalog.Lock()
	defer catalog.Unlock()
	spec, err := opSpec(op)
	if err != nil {
		panic(err)
	}
	name := spec.Name
	if name == "" || catalog.ops[name] != nil {
		panic("duplicate or empty op: " + name)
	}
	catalog.ops[name] = op
}
