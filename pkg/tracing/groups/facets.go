package groups

import (
	"context"
	"errors"
	"fmt"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

// ErrNotListed is a process the latest tick did not list.
var ErrNotListed = errors.New("not listed at the latest tick")

// Facets are what a process holds and runs with at one reading, as
// process_samples and process_tree record them: its context, its environment
// with its secrets masked, and its open descriptors by facet
// (process.Facets).
type Facets struct {
	PID     int       `json:"pid"`
	Key     string    `json:"key"`
	Name    string    `json:"name"`
	Command string    `json:"command"`
	ReadAt  time.Time `json:"readAt"`
	Context Context   `json:"context"`
	// EnvironmentError is why the environment could not be read, such as
	// another user's process.
	Environment      map[string]string                     `json:"environment,omitempty"`
	EnvironmentError string                                `json:"environmentError,omitempty"`
	Descriptors      map[string][]clickyprocess.Descriptor `json:"descriptors"`
}

// Facets reads the facets of a process the latest Tick listed.
func (t *Top) Facets(ctx context.Context, pid int) (Facets, error) {
	t.mu.Lock()
	var listed *ProcessSample
	for index := range t.processes {
		if t.processes[index].PID == pid {
			listed = &t.processes[index]
		}
	}
	if listed == nil {
		t.mu.Unlock()
		return Facets{}, fmt.Errorf("pid %d: %w", pid, ErrNotListed)
	}
	facets := Facets{PID: pid, Key: listed.Key, Name: listed.Name, Command: listed.Command}
	anchor := clickyprocess.Process{PID: pid, PPID: listed.PPID, Command: listed.Command}
	facets.Context = t.contexts.named(t.contexts.anchors(ctx, []clickyprocess.Process{anchor})[0])
	t.mu.Unlock()

	descriptors, err := clickyprocess.Descriptors(ctx, pid)
	if err != nil {
		return Facets{}, fmt.Errorf("read the descriptors of pid %d: %w", pid, err)
	}
	facets.Descriptors = process.FacetDescriptors(descriptors[pid])
	environment, err := clickyprocess.Environment(ctx, pid, clickyprocess.EnvironmentOptions{})
	if err != nil {
		facets.EnvironmentError = err.Error()
	} else {
		facets.Environment = tracing.MaskEnvironment(environment)
	}
	facets.ReadAt = time.Now().UTC()
	return facets, nil
}
