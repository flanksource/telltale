package groups

import (
	"fmt"
	"time"

	tracetarget "github.com/flanksource/telltale/pkg/tracing/target"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
)

// Kind is the name a host registers the process_groups kind as.
const Kind = "process_groups"

// DefaultInterval is how often groups are read unless asked otherwise: each
// tick lists the host and reads the disk and sockets of every grouped
// process.
const DefaultInterval = 2 * time.Second

// Params choose what a process_groups capture reads and how often.
type Params struct {
	// Interval, a duration such as 2s, is how often the groups are read.
	Interval string `json:"interval,omitempty" clicky:"title=Interval,desc=How often the groups are read such as 2s (at least 1s)"`
	All      bool   `json:"all,omitempty" clicky:"title=All processes,desc=Also record the processes no tool's group holds by program"`
	// Groups record only the groups of these rules, and those under them.
	Groups []string `json:"groups,omitempty" clicky:"title=Groups,desc=Record only these groups and those under them such as claude or vitest"`
	Net    bool     `json:"net,omitempty" clicky:"title=Network bytes,desc=Read each process's network bytes with nettop (darwin; costs over a core)"`
	// Catalog is a groups.yaml applied over the built-in rules.
	Catalog string             `json:"catalog,omitempty" clicky:"title=Catalog,desc=A groups.yaml whose rules are applied over the built-in ones"`
	Target  tracetarget.Target `json:"target,omitzero"`
}

func (p Params) Validate() error {
	_, err := p.interval()
	return err
}

func (p Params) interval() (time.Duration, error) {
	if p.Interval == "" {
		return DefaultInterval, nil
	}
	interval, err := time.ParseDuration(p.Interval)
	if err != nil {
		return 0, fmt.Errorf("interval %q is not a duration such as 2s", p.Interval)
	}
	if interval < time.Second {
		return 0, fmt.Errorf("interval %s is shorter than 1s", interval)
	}
	return interval, nil
}

// Options are the Top options the params ask for, with the catalog loaded.
func (p Params) Options() (Options, error) {
	interval, err := p.interval()
	if err != nil {
		return Options{}, err
	}
	var files []string
	if p.Catalog != "" {
		files = append(files, p.Catalog)
	}
	catalog, err := LoadCatalog(files...)
	if err != nil {
		return Options{}, err
	}
	return Options{Catalog: catalog, All: p.All, Groups: p.Groups, Net: p.Net, Interval: interval}, nil
}

type kind struct {
	sampler *process.Sampler
}

func (kind) Params() Params { return Params{} }

func (kind) Schema() recordresults.ResultType[GroupSample] {
	return recordresults.ResultType[GroupSample]{
		Title: "Process groups", TimeColumn: "sampledAt", Follow: true,
		Indexes:       []recordstore.IndexDef{{Columns: []string{"key", "sampledAt"}}, {Columns: []string{"tick"}}},
		SearchColumns: []string{"name", "command", "target"},
	}
}

type topKey struct{}

// Prepare starts the capture's Top, refusing a catalog or group it cannot
// read, and nettop where there is none, before the session starts.
func (k kind) Prepare(ctx dbcontext.Context, params Params, _ tracing.Emitter[GroupSample]) (dbcontext.Context, func() error, error) {
	options, err := params.Options()
	if err != nil {
		return ctx, nil, err
	}
	top, err := NewTop(ctx, k.sampler, options)
	if err != nil {
		return ctx, nil, err
	}
	return ctx.WithValue(topKey{}, top), func() error { top.Close(); return nil }, nil
}

// Handle records every shown group each interval until the capture stops.
func (kind) Handle(ctx dbcontext.Context, params Params, records tracing.Emitter[GroupSample], _ tracing.Records[GroupSample]) error {
	interval, err := params.interval()
	if err != nil {
		return err
	}
	top := ctx.Value(topKey{}).(*Top)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		frame, err := top.Tick(ctx)
		if err != nil {
			// A tick the stop cancelled (its ps or lsof killed) is the stop, not a failure.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, sample := range frame.Groups {
			if err := records.Emit(ctx, sample); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// NewKind is the process_groups kind over sampler, whose sessions can follow
// the groups as they are read.
func NewKind(sampler *process.Sampler) *tracing.Handler[Params, GroupSample] {
	return tracing.NewHandler[Params, GroupSample](kind{sampler: sampler}, tracing.Capabilities{Live: true}, "host")
}
