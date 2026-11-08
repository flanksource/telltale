package processcapture

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type Options struct {
	process.Options
	Environment *process.EnvironmentParams `json:"environment,omitempty"`
	Facets      []string                   `json:"facets,omitempty"`
	Catalog     string                     `json:"catalog,omitempty"`
	Net         bool                       `json:"net,omitempty"`
}

type Params struct {
	Options
	Target target.Target `json:"target,omitzero"`
}

func (p Params) Validate() error {
	if err := p.Options.Validate(); err != nil {
		return err
	}
	if p.Target.PID < 0 {
		return fmt.Errorf("invalid process pid %d", p.Target.PID)
	}
	for i, facet := range p.Facets {
		if !slices.Contains([]string{Files, Sockets, "connections"}, facet) || slices.Contains(p.Facets[:i], facet) {
			return fmt.Errorf("invalid or repeated process facet %q: expected files, sockets or connections", facet)
		}
	}
	return nil
}

func (p Params) root() int {
	if p.Target.Seam == target.Host {
		return 0
	}
	if p.Target.PID == 0 {
		return os.Getpid()
	}
	return p.Target.PID
}

type KindOptions struct{ Dumpers process.TransportLister }
type collector struct {
	sampler *process.Sampler
	options KindOptions
}
type captureKey struct{}
type capture struct {
	mu       sync.Mutex
	store    *Store
	top      *groups.Top
	params   Params
	sampler  *process.Sampler
	rootGone bool
	dumpers  process.TransportLister
}

func (collector) Params() Params { return Params{} }
func (collector) Schema() recordresults.ResultType[Identity] {
	return recordresults.ResultType[Identity]{Title: "Processes", TimeColumn: "firstObservedAt", KeyColumn: "key", OnConflict: recordstore.OnConflictReplace,
		Hierarchy: &recordresults.HierarchyColumns{ID: "key", Parent: "parentKey"}, SearchColumns: []string{"name", "command"}}
}

func (c collector) Prepare(ctx dbcontext.Context, params Params, _ tracing.Emitter[Identity]) (dbcontext.Context, func() error, error) {
	backend, err := tracing.CaptureBackend(ctx)
	if err != nil {
		return ctx, nil, err
	}
	store, err := NewStore(StoreOptions{Backend: backend, Stream: tracing.StreamOf(ctx)})
	if err != nil {
		return ctx, nil, err
	}
	interval := time.Second
	if params.Interval != "" {
		interval, err = time.ParseDuration(params.Interval)
	}
	if err != nil {
		return ctx, nil, err
	}
	var paths []string
	if params.Catalog != "" {
		paths = append(paths, params.Catalog)
	} else {
		path, err := groups.CatalogFile()
		if err != nil {
			return ctx, nil, err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	catalog, err := groups.LoadCatalog(paths...)
	if err != nil {
		return ctx, nil, err
	}
	top, err := groups.NewTop(ctx, c.sampler, groups.TopOptions{Catalog: catalog, All: true, RootPID: params.root(), Interval: interval, Net: params.Net, SkipDescriptors: len(params.Facets) == 0})
	if err != nil {
		return ctx, nil, err
	}
	run := &capture{store: store, top: top, params: params, sampler: c.sampler, dumpers: c.options.Dumpers}
	if err := run.observe(ctx); err != nil {
		top.Close()
		return ctx, nil, err
	}
	if params.root() > 0 && run.rootGone {
		top.Close()
		return ctx, nil, fmt.Errorf("process %d is not running", params.root())
	}
	return ctx.WithValue(captureKey{}, run), func() error { top.Close(); return store.Close(context.WithoutCancel(ctx)) }, nil
}

func (collector) Handle(ctx dbcontext.Context, params Params, _ tracing.Emitter[Identity], _ tracing.Records[Identity]) error {
	run := ctx.Value(captureKey{}).(*capture)
	interval := time.Second
	if params.Interval != "" {
		var err error
		interval, err = time.ParseDuration(params.Interval)
		if err != nil {
			return err
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return run.observe(context.WithoutCancel(ctx))
		case <-ticker.C:
			if err := run.observe(ctx); err != nil {
				if ctx.Err() != nil {
					return run.observe(context.WithoutCancel(ctx))
				}
				return err
			}
			run.mu.Lock()
			gone := run.rootGone
			run.mu.Unlock()
			if gone {
				return nil
			}
		}
	}
}

func (collector) Checkpoint(ctx dbcontext.Context, _ Params, _ tracing.Emitter[Identity]) error {
	return ctx.Value(captureKey{}).(*capture).observe(ctx)
}

// NewKind records one process table, resource deltas, and a wide metrics table.
func NewKind(sampler *process.Sampler, options KindOptions) *tracing.Handler[Params, Identity] {
	return tracing.NewHandler[Params, Identity](collector{sampler: sampler, options: options}, tracing.Capabilities{Live: true}, target.Process, target.Host).WithResults(
		tracing.NewResultTable("facets", recordresults.ResultType[FacetEvent]{Title: "Process facets", TimeColumn: "observedAt", Follow: true, Indexes: []recordstore.IndexDef{{Columns: []string{"process", "facet", "entity", "tick"}}}}),
		tracing.NewResultTable("metrics", recordresults.ResultType[MetricRow]{Title: "Process metrics", TimeColumn: "observedAt", Dynamic: true, Follow: true, Indexes: []recordstore.IndexDef{{Columns: []string{"entity", "observedAt"}}}}),
	)
}

func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.backend.AppendBatch(ctx, recordstore.Batch{ID: s.stream + "-close", Atomic: true, Producer: recordstore.Producer{Instance: s.stream, Seq: s.tick + 1}, Entries: []recordstore.BatchEntry{
		{Op: recordstore.BatchSeal, Stream: s.stream + "-facets"}, {Op: recordstore.BatchSeal, Stream: s.stream + "-metrics"},
	}})
	return err
}
