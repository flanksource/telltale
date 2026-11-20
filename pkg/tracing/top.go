package tracing

import (
	"context"
	"fmt"
	"slices"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/otlp/profilepublication"
)

type TopMetric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit,omitempty"`
	View  string `json:"view"`
}

type TopSpec struct {
	Relation         *TopRelation                                                                        `json:"relation,omitempty"`
	Hierarchy        func([]query.Row, string) (*sampleframe.Hierarchy, error)                           `json:"-"`
	Profile          func(context.Context, *Runtime, TraceRef, sampleframe.Frame) (query.Profile, error) `json:"-"`
	ProjectionColumn string                                                                              `json:"projectionColumn,omitempty"`
	PerSource        bool                                                                                `json:"perSource,omitempty"`
	Result           string                                                                              `json:"result,omitempty"`
	Stack            *TopStack                                                                           `json:"stack,omitempty"`
	Identity         []string                                                                            `json:"identity"`
	Order            query.Order                                                                         `json:"order"`
	Metrics          []TopMetric                                                                         `json:"metrics,omitempty"`
	GroupBy          []string                                                                            `json:"groupBy,omitempty"`
}

type TopRelation struct {
	Column string `json:"column"`
	Target string `json:"target"`
	Label  string `json:"label"`
}

type TopStack struct {
	Column       string `json:"column"`
	MetricColumn string `json:"metricColumn"`
	FrameKey     string `json:"frameKey,omitempty"`
	Separator    string `json:"separator,omitempty"`
	LeafFirst    bool   `json:"leafFirst,omitempty"`
}

func (h *Handler[P, R]) WithTop(spec TopSpec) *Handler[P, R] {
	h.top = &spec
	return h.WithResults(NewResultTable("frames", recordresults.ResultType[sampleframe.Frame]{Title: "Completed samples", TimeColumn: "completedAt", Follow: true}))
}

func (h *Handler[P, R]) Top() *TopSpec { return h.top }

func TopOf(plugin TracePlugin) *TopSpec {
	if provider, ok := plugin.(interface{ Top() *TopSpec }); ok {
		return provider.Top()
	}
	return nil
}

type frameEmitter[R any] interface {
	EmitFrame(context.Context, sampleframe.Frame, []R) error
}

// PublishFrame refuses emitters without atomic sample support.
func PublishFrame[R any](ctx context.Context, out Emitter[R], frame sampleframe.Frame, rows []R) error {
	writer, ok := out.(frameEmitter[R])
	if !ok {
		return fmt.Errorf("sampled capture requires an atomic frame emitter")
	}
	return writer.EmitFrame(ctx, frame, rows)
}

func (e *emitter[R]) EmitFrame(ctx context.Context, frame sampleframe.Frame, records []R) error {
	if e.frames == nil {
		return fmt.Errorf("kind does not declare a Top capability")
	}
	rows := make([]recordstore.Row, len(records))
	for i, record := range records {
		row, err := e.encode(record)
		if err != nil {
			return err
		}
		rows[i] = row
	}
	if e.top.Stack != nil {
		metrics := []TopMetric{}
		for _, row := range rows {
			key, _ := row[e.top.Stack.MetricColumn].(string)
			index := slices.IndexFunc(e.top.Metrics, func(metric TopMetric) bool { return metric.Key == key })
			if index < 0 {
				return fmt.Errorf("unregistered sample metric %q", key)
			}
			metric := e.top.Metrics[index]
			if unit := row["unit"]; unit != metric.Unit {
				return fmt.Errorf("sample metric %s unit %v disagrees with %s", key, unit, metric.Unit)
			}
			if !slices.ContainsFunc(metrics, func(m TopMetric) bool { return m.Key == key }) {
				metrics = append(metrics, metric)
			}
		}
		if frame.Summary == nil {
			frame.Summary = map[string]any{}
		}
		slices.SortFunc(metrics, func(a, b TopMetric) int {
			return slices.IndexFunc(e.top.Metrics, func(m TopMetric) bool { return m.Key == a.Key }) - slices.IndexFunc(e.top.Metrics, func(m TopMetric) bool { return m.Key == b.Key })
		})
		frame.Summary["metrics"] = metrics
	}
	if profilepublication.IsProfile(frame) {
		published, err := e.publishProfile(ctx, frame, rows)
		if err != nil || !published {
			return err
		}
	} else {
		if _, err := e.frames.Append(ctx, frame, rows); err != nil {
			return err
		}
	}
	e.source.mu.Lock()
	e.source.summary.Emitted += int64(len(rows))
	e.source.mu.Unlock()
	return nil
}

func (e *emitter[R]) publishProfile(ctx context.Context, frame sampleframe.Frame, rows []recordstore.Row) (bool, error) {
	backend, ok := e.frames.Backend.(profilepublication.Backend)
	if !ok {
		return false, fmt.Errorf("profile capture requires durable identity reads and atomic publication")
	}
	frames, err := sampleframe.Schema(e.frames.Schema.Kind + "_frames")
	if err != nil {
		return false, err
	}
	return profilepublication.Append(ctx, backend, profilepublication.Publication{Kind: e.frames.Schema.Kind, Stream: e.frames.Stream, FrameStream: e.frames.Stream + "-frames", Schema: e.frames.Schema, FrameSchema: frames, Frame: frame, Rows: rows})
}

func (s TopSpec) validate(schema recordstore.KindSchema) error {
	keys := append(slices.Clone(s.Identity), s.GroupBy...)
	if s.ProjectionColumn != "" {
		keys = append(keys, s.ProjectionColumn)
	}
	if s.Relation != nil {
		keys = append(keys, s.Relation.Column, s.Relation.Target)
	}
	if s.Stack != nil {
		keys = append(keys, s.Stack.Column, s.Stack.MetricColumn)
	}
	for _, key := range keys {
		if !slices.ContainsFunc(schema.Columns, func(c query.ColumnDef) bool { return c.Name == key }) {
			return fmt.Errorf("unknown Top identity column %q", key)
		}
	}
	metrics := map[string]bool{}
	for _, metric := range s.Metrics {
		if metric.Key == "" || metric.Label == "" || metrics[metric.Key] || !slices.Contains([]string{"table", "flamegraph", "treemap"}, metric.View) {
			return fmt.Errorf("invalid Top metric %q", metric.Key)
		}
		metrics[metric.Key] = true
	}
	for _, order := range s.Order {
		if !slices.ContainsFunc(schema.Columns, func(c query.ColumnDef) bool { return c.Name == order.Column }) {
			return fmt.Errorf("unknown Top order column %q", order.Column)
		}
	}
	return nil
}
