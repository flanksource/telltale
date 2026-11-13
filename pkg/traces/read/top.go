package read

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/flanksource/clicky/api"
	"slices"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
)

var ErrNoCompleteSamples = errors.New("complete samples unavailable; raw history is still available")

type TopQuery struct {
	Frame  string `json:"frame,omitempty"`
	Source string `json:"source,omitempty"`
	Metric string `json:"metric,omitempty"`
	Query
}

type TopResult struct {
	Selection  string                 `json:"selection"`
	Metrics    []tracing.TopMetric    `json:"metrics,omitempty"`
	Frames     []sampleframe.Frame    `json:"frames"`
	Display    api.TextTable          `json:"-"`
	Hierarchy  *sampleframe.Hierarchy `json:"hierarchy,omitempty"`
	Metric     string                 `json:"metric,omitempty"`
	Frame      sampleframe.Frame      `json:"frame"`
	Rows       []query.Row            `json:"rows"`
	Table      json.RawMessage        `json:"table"`
	Count      int                    `json:"count"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

func Frames(ctx context.Context, ref Ref) ([]sampleframe.Frame, error) {
	frames := []sampleframe.Frame{}
	err := ref.Runtime.Results.Backend.Scan(ctx, ref.Stream+"-frames", 0, func(_ int64, row recordstore.Row) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var frame sampleframe.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return err
		}
		if frame.Error == "" {
			frames = append(frames, frame)
		}
		return nil
	})
	if errors.Is(err, recordstore.ErrNotFound) || (err == nil && len(frames) == 0) {
		return nil, ErrNoCompleteSamples
	}
	return frames, err
}

func Top(ctx context.Context, ref Ref, q TopQuery) (TopResult, error) {
	if err := q.Validate(); err != nil {
		return TopResult{}, err
	}
	plugin, ok := ref.Runtime.Kinds.Get(ref.Kind)
	if !ok || tracing.TopOf(plugin) == nil {
		return TopResult{}, fmt.Errorf("kind %s does not support Top", ref.Kind)
	}
	spec := tracing.TopOf(plugin)
	frames, err := Frames(ctx, ref)
	if err != nil {
		return TopResult{}, err
	}
	if spec.Result == "" {
		frames = slices.DeleteFunc(frames, func(frame sampleframe.Frame) bool {
			return frame.AfterSeq < ref.AfterSeq || ref.ToSeq > 0 && frame.ToSeq > ref.ToSeq
		})
	}
	requested := strings.Split(q.Frame, ",")
	if len(requested) > 1 && !spec.PerSource {
		return TopResult{}, fmt.Errorf("kind requires a single completed sample")
	}
	index := slices.IndexFunc(frames, func(f sampleframe.Frame) bool {
		return f.ID == requested[0] && (q.Source == "" || f.Source == q.Source)
	})
	if q.Frame == "" {
		for i := len(frames) - 1; i >= 0; i-- {
			if q.Source == "" || frames[i].Source == q.Source {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return TopResult{}, fmt.Errorf("sample %q for source %q not found", q.Frame, q.Source)
	}
	frame := frames[index]
	if spec.Stack != nil {
		data, err := json.Marshal(frame.Summary["metrics"])
		if err != nil {
			return TopResult{}, err
		}
		copy := *spec
		if err := json.Unmarshal(data, &copy.Metrics); err != nil {
			return TopResult{}, err
		}
		spec = &copy
		if q.Metric == "" && len(spec.Metrics) > 0 {
			q.Metric = spec.Metrics[0].Key
		}
	}
	selectedFrames := []sampleframe.Frame{frame}
	if len(requested) > 1 {
		seen := map[string]bool{frame.Source: true}
		for _, id := range requested[1:] {
			i := slices.IndexFunc(frames, func(f sampleframe.Frame) bool { return f.ID == id && (q.Source == "" || q.Source == f.Source) })
			if i < 0 || seen[frames[i].Source] {
				return TopResult{}, fmt.Errorf("invalid sample selection %q", q.Frame)
			}
			selectedFrames = append(selectedFrames, frames[i])
			seen[frames[i].Source] = true
		}
	}
	if spec.PerSource && q.Source == "" && q.Frame == "" {
		selectedFrames = nil
		seen := map[string]bool{}
		for i := len(frames) - 1; i >= 0; i-- {
			if !seen[frames[i].Source] {
				selectedFrames = append(selectedFrames, frames[i])
				seen[frames[i].Source] = true
			}
		}
	}
	window := ref
	window.AfterSeq, window.ToSeq = frame.AfterSeq, frame.ToSeq
	if spec.Result != "" {
		window.Kind = ref.Kind + "_" + spec.Result
		window.Stream = ref.Stream + "-" + spec.Result
	}
	selection := q.Query
	selection.order = append(slices.Clone(spec.Order), query.OrderBy{Column: "seq", Unique: true})
	if spec.Hierarchy != nil && q.Sort == "" {
		metric, err := selectMetric(nil, spec, q.Metric)
		if err != nil {
			return TopResult{}, err
		}
		selection.order = append(query.Order{{Column: metric, Desc: true}}, slices.DeleteFunc(selection.order, func(by query.OrderBy) bool { return by.Column == metric })...)
	}
	selection.All, selection.Cursor, selection.NextCursor = true, "", nil
	rows := []query.Row{}
	profile, err := sampleProfile(ctx, window, spec, frame)
	if err != nil {
		return TopResult{}, err
	}
	selection.profile = &profile
	if spec.ProjectionColumn != "" {
		selection.order = profile.Order
	}
	if _, err := q.params(window, profile); err != nil {
		return TopResult{}, err
	}
	if _, err := profile.RequestedOrder(q.Sort, q.Desc); err != nil {
		return TopResult{}, err
	}
	for _, sample := range selectedFrames {
		if sample.Retained == 0 {
			continue
		}
		member := window
		member.AfterSeq, member.ToSeq = sample.AfterSeq, sample.ToSeq
		part, err := query.CollectRows(Rows(ctx, []Ref{member}, selection))
		if err != nil {
			return TopResult{}, err
		}
		rows = append(rows, part...)
	}
	if len(selectedFrames) > 1 {
		order := selection.order
		if q.Sort != "" {
			order = append(query.Order{{Column: q.Sort, Desc: q.Desc}}, order...)
		}
		rows = query.SortRows(rows, order)
	}
	metric, err := selectMetric(rows, spec, q.Metric)
	if err != nil {
		return TopResult{}, err
	}
	if spec.Stack != nil {
		rows = slices.DeleteFunc(rows, func(row query.Row) bool { return row[spec.Stack.MetricColumn] != metric })
	}
	hierarchy, err := topHierarchy(rows, spec, metric)
	if err != nil {
		return TopResult{}, err
	}
	scope, err := cursorScope([]Ref{window}, selection)
	if err != nil {
		return TopResult{}, err
	}
	for _, sample := range selectedFrames {
		scope += "/" + sample.ID
	}
	scope += "/" + metric
	offset, err := topOffset(q.Cursor, scope)
	if err != nil || offset > len(rows) {
		return TopResult{}, fmt.Errorf("invalid sample cursor")
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	end := offset + min(len(rows)-offset, limit)
	if q.All {
		end = len(rows)
	}
	ids := make([]string, len(selectedFrames))
	for i, sample := range selectedFrames {
		ids[i] = sample.ID
	}
	result := TopResult{Selection: strings.Join(ids, ","), Metrics: spec.Metrics, Frame: frame, Frames: selectedFrames, Rows: rows[offset:end], Count: len(rows), Hierarchy: hierarchy, Metric: metric}
	if end < len(rows) {
		data, err := json.Marshal(topPosition{Scope: scope, Offset: end})
		if err != nil {
			return TopResult{}, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	filterKeys, err := profile.ColumnFilterKeys()
	if err != nil {
		return TopResult{}, err
	}
	sortKeys, err := profile.ColumnSortKeys()
	if err != nil {
		return TopResult{}, err
	}
	display := &query.Result{Rows: result.Rows, Presenter: profile.Presenter, ColumnFilterKeys: filterKeys, ColumnSortKeys: sortKeys}
	result.Display, err = display.DisplayTable(profile.Columns)
	if err != nil {
		return TopResult{}, err
	}
	document, err := display.Render(profile.Columns, "clicky-json")
	result.Table = json.RawMessage(document)
	return result, err
}

type topPosition struct {
	Scope  string
	Offset int
}

func topOffset(cursor, scope string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	var pos topPosition
	if err := json.Unmarshal(data, &pos); err != nil {
		return 0, err
	}
	if pos.Scope != scope || pos.Offset < 0 {
		return 0, fmt.Errorf("cursor belongs to another sample or filter")
	}
	return pos.Offset, nil
}
