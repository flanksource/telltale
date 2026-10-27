package traces

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// KindDeps are what a run's kinds share: one process sampler and the stack
// transports.
type KindDeps struct {
	Dumpers *stacks.Dumpers
	Sampler *process.Sampler
}

// Example is one invocation a kind's help shows, as the args after
// `telltale run <kind>`, and what it does.
type Example struct {
	Args string
	Says string
}

// Registration is one trace kind of the catalog every run store serves and
// the CLI is generated from.
type Registration struct {
	Name string
	// Title names the kind in a start form and in help.
	Title string
	// Summary is one line on what the kind records.
	Summary string
	// Description says what the kind records of a traced command and what
	// it does not.
	Description string
	Seams       []target.Seam
	DefaultSeam target.Seam
	// Exports are the environment variables a capture hands the command.
	Exports  []string
	Examples []Example
	// Armable kinds are offered by the start form.
	Armable bool
	// Checkpoint are the checkpoint params a run reads the kind with, every
	// interval it is given and as an attached run stops; nil for a kind
	// that records without checkpoints.
	Checkpoint any
	New        func(KindDeps) tracing.TracePlugin
}

func (r Registration) validate() error {
	var errs []error
	if strings.TrimSpace(r.Name) == "" {
		errs = append(errs, errors.New("a name is required"))
	}
	if r.New == nil {
		errs = append(errs, errors.New("a New func is required"))
	}
	for _, seam := range r.Seams {
		if !slices.Contains([]target.Seam{target.Self, target.Agent, target.Process, target.Proxy, target.Host}, seam) {
			errs = append(errs, fmt.Errorf("unknown seam %q", seam))
		}
	}
	if !slices.Contains(r.Seams, r.DefaultSeam) {
		errs = append(errs, fmt.Errorf("default seam %q must be among supported seams %v", r.DefaultSeam, r.Seams))
	}
	if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Summary) == "" {
		errs = append(errs, errors.New("a title and a summary are required"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("register trace kind %q: %w", r.Name, err)
	}
	return nil
}

var registry = struct {
	mu    sync.RWMutex
	kinds map[string]Registration
}{kinds: map[string]Registration{}}

// Register adds a kind to the catalog. Register it in an init func, so it is
// in the catalog before the CLI is generated from it.
func Register(registration Registration) error {
	if err := registration.validate(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, ok := registry.kinds[registration.Name]; ok {
		return fmt.Errorf("register trace kind %q: it is registered twice", registration.Name)
	}
	registry.kinds[registration.Name] = registration
	return nil
}

// MustRegister is Register for an init func, panicking on a registration
// the catalog refuses.
func MustRegister(registration Registration) {
	if err := Register(registration); err != nil {
		panic(err)
	}
}

// Registrations are the registered kinds, sorted by name.
func Registrations() []Registration {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	registrations := make([]Registration, 0, len(registry.kinds))
	for _, registration := range registry.kinds {
		registrations = append(registrations, registration)
	}
	slices.SortFunc(registrations, func(a, b Registration) int { return cmp.Compare(a.Name, b.Name) })
	return registrations
}

// Registered is the registration of kind, refusing one not registered by
// naming those that are.
func Registered(kind string) (Registration, error) {
	registry.mu.RLock()
	registration, ok := registry.kinds[kind]
	registry.mu.RUnlock()
	if !ok {
		names := make([]string, 0)
		for _, registration := range Registrations() {
			names = append(names, registration.Name)
		}
		return Registration{}, fmt.Errorf("unknown trace kind %q (registered: %s)", kind, strings.Join(names, ", "))
	}
	return registration, nil
}
