// Package process holds the process trace kinds: process_tree records each
// process under a root as it starts, changes and exits, and process_samples
// records the CPU, memory, disk IO and armed facets of every one of them at
// each tick.
package process

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// MinInterval is the shortest tick a capture takes: each tick lists every
// process on the host.
const MinInterval = 250 * time.Millisecond

const defaultInterval = time.Second

// Options choose how often a capture looks at the tree it follows.
type Options struct {
	// Interval, a duration such as 1s, is how often the tree is sampled.
	// Empty is a second.
	Interval string `json:"interval,omitempty" clicky:"title=Interval,desc=How often the tree is sampled such as 1s (at least 250ms)"`
}

// Validate is the static check of the interval.
func (p Options) Validate() error {
	_, err := p.interval()
	return err
}

// validateRoot refuses a target that names no process id.
func validateRoot(t target.Target) error {
	if t.PID < 0 {
		return fmt.Errorf("pid %d is not a process id", t.PID)
	}
	return nil
}

func (p Options) interval() (time.Duration, error) {
	if p.Interval == "" {
		return defaultInterval, nil
	}
	interval, err := time.ParseDuration(p.Interval)
	if err != nil {
		return 0, fmt.Errorf("interval %q is not a duration such as 1s", p.Interval)
	}
	if interval < MinInterval {
		return 0, fmt.Errorf("interval %s is shorter than %s", interval, MinInterval)
	}
	return interval, nil
}

// rootOf is the process a capture aimed at t follows: t's, else this one.
func rootOf(t target.Target) int {
	if t.PID == 0 {
		return os.Getpid()
	}
	return t.PID
}

// TreeOptions are what process_tree chooses beside its root: how often it
// looks, and which environment variables each process's node is read with.
type TreeOptions struct {
	Options
	// Environment reads each process's environment once, as its node is
	// created, limited to these keys and prefixes; empty ones read all of it.
	// Absent reads none.
	Environment *EnvironmentParams `json:"environment,omitempty" clicky:"title=Environment,desc=Environment variables read onto each process"`
}

// TreeParams are the params of process_tree: its options on the tree its
// target roots.
type TreeParams struct {
	TreeOptions
	Target target.Target `json:"target,omitzero"`
}

func (p TreeParams) Validate() error {
	return errors.Join(validateRoot(p.Target), p.TreeOptions.Validate())
}

func (p TreeParams) root() int { return rootOf(p.Target) }

// EnvironmentParams name the environment variables a node is read with.
type EnvironmentParams struct {
	Keys     []string `json:"keys,omitempty" clicky:"title=Keys,desc=Environment variables to read onto each process"`
	Prefixes []string `json:"prefixes,omitempty" clicky:"title=Prefixes,desc=Prefixes of the environment variables to read onto each process"`
}

func (p EnvironmentParams) options() clickyprocess.EnvironmentOptions {
	return clickyprocess.EnvironmentOptions{Keys: p.Keys, Prefixes: p.Prefixes}
}

// The facets process_samples reads of each process's open descriptors.
const (
	// FacetOpenFiles are the descriptors that are no socket.
	FacetOpenFiles = "openFiles"
	// FacetSockets are the internet and unix-domain sockets.
	FacetSockets = "sockets"
	// FacetPorts are the sockets listening on a port.
	FacetPorts = "ports"
	// FacetConnections are the TCP and UDP sockets with a peer.
	FacetConnections = "connections"
)

// Facets are every facet a capture can arm.
var Facets = []string{FacetOpenFiles, FacetSockets, FacetPorts, FacetConnections}

// SampleOptions are what process_samples chooses beside its root: how often
// it samples, and the facets read of every process at each tick.
type SampleOptions struct {
	Options
	// Facets are read of every process at each tick, with one lsof call for
	// the whole tree: each one's count every tick, its list when it changed.
	Facets []string `json:"facets,omitempty" clicky:"title=Facets,desc=Descriptors read of every process each tick with lsof: openFiles sockets ports or connections"`
}

// Validate is the static check of the interval and the facets.
func (p SampleOptions) Validate() error {
	var errs []error
	for index, facet := range p.Facets {
		switch {
		case !slices.Contains(Facets, facet):
			errs = append(errs, fmt.Errorf("facet %q: expected one of %v", facet, Facets))
		case slices.Contains(p.Facets[:index], facet):
			errs = append(errs, fmt.Errorf("facet %q twice", facet))
		}
	}
	return errors.Join(append(errs, p.Options.Validate())...)
}

// SampleParams are the params of process_samples: its options on the tree its
// target roots.
type SampleParams struct {
	SampleOptions
	Target target.Target `json:"target,omitzero"`
}

func (p SampleParams) Validate() error {
	return errors.Join(p.SampleOptions.Validate(), validateRoot(p.Target))
}

func (p SampleParams) root() int { return rootOf(p.Target) }

func (p SampleParams) armed(facet string) bool { return slices.Contains(p.Facets, facet) }

// errRootGone ends a capture whose root has exited.
var errRootGone = errors.New("root process exited")
