package processcapture

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/metrics"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	sdk "github.com/flanksource/telltale/sdk"
)

const Kind = "process"
const FacetsTable = "process_facets"
const MetricsTable = "process_metrics"

type Identity struct {
	Key              string                   `json:"key"`
	ParentKey        string                   `json:"parentKey,omitempty"`
	PID              int                      `json:"pid"`
	PPID             int                      `json:"ppid"`
	Name             string                   `json:"name"`
	Command          string                   `json:"command"`
	CWD              string                   `json:"cwd,omitempty"`
	StartedAt        time.Time                `json:"startedAt"`
	FirstObservedAt  time.Time                `json:"firstObservedAt"`
	ExitedAt         *time.Time               `json:"exitedAt,omitempty"`
	Environment      map[string]string        `json:"environment,omitempty"`
	Tags             map[string]string        `json:"tags,omitempty"`
	EnvironmentError string                   `json:"environmentError,omitempty"`
	Groups           []GroupInfo              `json:"groups,omitempty"`
	StackTransports  []process.StackTransport `json:"stackTransports,omitempty"`
}

type GroupInfo struct {
	Key          string         `json:"key"`
	ParentKey    string         `json:"parentKey,omitempty"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Target       string         `json:"target,omitempty"`
	PID          int            `json:"pid"`
	Command      string         `json:"command,omitempty"`
	Context      groups.Context `json:"context"`
	Origin       groups.Origin  `json:"origin"`
	SkipAncestry bool           `json:"skipAncestry,omitempty"`
}

type Observation struct {
	At        time.Time
	Tick      int64
	Processes []Identity
	Facets    []FacetObservation
	Metrics   []metrics.Sample
}

type StoreOptions struct {
	Backend recordstore.BatchAppender
	Stream  string
}

type Store struct {
	mu         sync.Mutex
	backend    recordstore.BatchAppender
	stream     string
	metrics    *metrics.DynamicMetricTable
	facets     *FacetState
	tick       int64
	identities map[string]Identity
}

func NewStore(options StoreOptions) (*Store, error) {
	if options.Backend == nil {
		return nil, fmt.Errorf("process capture requires an atomic batch backend")
	}
	if err := recordstore.ValidateStream(options.Stream); err != nil {
		return nil, err
	}
	table, err := metricTable()
	if err != nil {
		return nil, err
	}
	return &Store{backend: options.Backend, stream: options.Stream, metrics: table, facets: NewFacetState(), identities: map[string]Identity{}}, nil
}

func metricTable() (*metrics.DynamicMetricTable, error) {
	return metrics.NewDynamicMetricTable(metrics.TableOptions{Kind: MetricsTable, Metrics: []metrics.Definition{
		{Name: "cpuPercent", Unit: "percent"}, {Name: "rssBytes", Unit: "bytes"}, {Name: "memoryPercent", Unit: "percent"},
		{Name: "diskReadBytes", Unit: "bytes", Counter: true}, {Name: "diskWriteBytes", Unit: "bytes", Counter: true},
		{Name: "openFiles"}, {Name: "receivingSockets"}, {Name: "connectionsIn"}, {Name: "connectionsOut"}, {Name: "connectionsUnknown"},
		{Name: "diskReadBps", Unit: "Bps"}, {Name: "diskWriteBps", Unit: "Bps"}, {Name: "netRxBps", Unit: "Bps"}, {Name: "netTxBps", Unit: "Bps"},
		{Name: "swapBytes", Unit: "bytes"}, {Name: "compressedBytes", Unit: "bytes"},
	}})
}

func Schemas() ([]recordstore.KindSchema, error) {
	identity, err := query.ColumnsFor(reflect.TypeFor[Identity]())
	if err != nil {
		return nil, err
	}
	facets, err := query.ColumnsFor(reflect.TypeFor[FacetEvent]())
	if err != nil {
		return nil, err
	}
	table, err := metricTable()
	if err != nil {
		return nil, err
	}
	return []recordstore.KindSchema{
		{Kind: Kind, Columns: identity, Options: recordstore.KindOptions{Key: "key", OnConflict: recordstore.OnConflictReplace, TimeColumn: "firstObservedAt"}},
		{Kind: FacetsTable, Columns: facets, Options: recordstore.KindOptions{TimeColumn: "observedAt", Indexes: []recordstore.IndexDef{{Columns: []string{"process", "facet", "entity", "tick"}}}}},
		table.Schema(),
	}, nil
}

func (s *Store) Apply(ctx context.Context, o Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.At.IsZero() || o.Tick != s.tick+1 {
		return fmt.Errorf("process observation must advance tick %d to %d with a timestamp", s.tick, s.tick+1)
	}
	schemas, err := Schemas()
	if err != nil {
		return err
	}
	batch := recordstore.Batch{ID: fmt.Sprintf("%s-%d", s.stream, o.Tick), Atomic: true, Producer: recordstore.Producer{Instance: s.stream, Seq: o.Tick}, Schemas: schemas[:2]}
	identities, rows, err := s.processRows(o.Processes)
	if err != nil {
		return err
	}
	batch.Entries = append(batch.Entries, recordstore.BatchEntry{Op: recordstore.BatchAppend, Kind: Kind, Stream: s.stream, Rows: rows})
	next := s.facets.clone()
	counts := map[string]map[string]any{}
	events := []recordstore.Row{}
	for _, facet := range o.Facets {
		if _, ok := identities[facet.Process]; !ok {
			return fmt.Errorf("facets reference unknown process %q", facet.Process)
		}
		facet.At, facet.Tick = o.At, o.Tick
		changes := next.diff(facet)
		if facet.Complete {
			counts[facet.Process] = next.counts(facet)
		}
		for _, change := range changes {
			row, err := recordstore.EncodeRow(change)
			if err != nil {
				return err
			}
			events = append(events, row)
		}
	}
	batch.Entries = append(batch.Entries, recordstore.BatchEntry{Op: recordstore.BatchAppend, Kind: FacetsTable, Stream: s.stream + "-facets", Rows: events})
	source := metrics.DynamicMetricSource{Table: s.metrics, Stream: s.stream + "-metrics"}
	samples := slices.Clone(o.Metrics)
	for i := range samples {
		if _, ok := identities[samples[i].Entity]; !ok {
			return fmt.Errorf("metrics reference unknown process %q", samples[i].Entity)
		}
		samples[i].At, samples[i].Tick = o.At, o.Tick
		values := map[string]any{}
		maps.Copy(values, samples[i].Values)
		samples[i].Values = values
		maps.Copy(samples[i].Values, counts[samples[i].Entity])
	}
	if _, err := source.Append(ctx, metrics.AppendOptions{Backend: s.backend, Batch: batch, Samples: samples}); err != nil {
		return err
	}
	s.identities, s.facets, s.tick = identities, next, o.Tick
	return nil
}

func (s *Store) processRows(processes []Identity) (map[string]Identity, []recordstore.Row, error) {
	identities := maps.Clone(s.identities)
	rows := []recordstore.Row{}
	for _, process := range processes {
		if process.Key == "" || process.PID <= 0 || process.FirstObservedAt.IsZero() {
			return nil, nil, fmt.Errorf("process identity requires key, pid and first observation time")
		}
		if previous, ok := identities[process.Key]; ok {
			if previous.PID != process.PID {
				return nil, nil, fmt.Errorf("process identity %s changed pid", process.Key)
			}
			if previous.ExitedAt != nil || process.ExitedAt == nil {
				continue
			}
			previous.ExitedAt = process.ExitedAt
			process = previous
		}
		process.Command = sdk.Mask(process.Command)
		process.Environment = tracing.MaskEnvironment(process.Environment)
		process.Groups = slices.Clone(process.Groups)
		process.StackTransports = slices.Clone(process.StackTransports)
		if process.ExitedAt != nil {
			exited := *process.ExitedAt
			process.ExitedAt = &exited
		}
		for i := range process.Groups {
			process.Groups[i].Command = sdk.Mask(process.Groups[i].Command)
		}
		row, err := recordstore.EncodeRow(process)
		if err != nil {
			return nil, nil, err
		}
		process.Environment = maps.Clone(process.Environment)
		process.Tags = maps.Clone(process.Tags)
		identities[process.Key] = process
		rows = append(rows, row)
	}
	return identities, rows, nil
}
