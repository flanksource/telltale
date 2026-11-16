package read

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
)

func sampleProfile(ctx context.Context, ref Ref, spec *tracing.TopSpec, frame sampleframe.Frame) (query.Profile, error) {
	if spec.Profile != nil {
		return spec.Profile(ctx, ref.Runtime, ref.TraceRef, frame)
	}
	_, profile, err := ref.profile(ctx, "")
	if err != nil || spec.ProjectionColumn == "" {
		return profile, err
	}
	data, err := json.Marshal(frame.Summary)
	if err != nil {
		return query.Profile{}, err
	}
	var metadata struct {
		Columns []query.ColumnDef
		Order   query.Order
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return query.Profile{}, err
	}
	return recordresults.ProjectJSON(profile, spec.ProjectionColumn, metadata.Columns, metadata.Order)
}

// TopProfile exposes the same resolved sample columns used by the Top reader.
func TopProfile(ctx context.Context, ref Ref, q TopQuery) (query.Profile, error) {
	result, err := Top(ctx, ref, TopQuery{Frame: q.Frame, Source: q.Source, Metric: q.Metric, Query: Query{Limit: 1}})
	if err != nil {
		return query.Profile{}, err
	}
	plugin, _ := ref.Runtime.Kinds.Get(ref.Kind)
	spec := tracing.TopOf(plugin)
	if spec.Result != "" {
		ref.Kind += "_" + spec.Result
		ref.Stream += "-" + spec.Result
	}
	profile, err := sampleProfile(ctx, ref, spec, result.Frame)
	if err != nil {
		return query.Profile{}, err
	}
	if profile.Presenter != nil {
		columns := slices.Clone(profile.Columns)
		profile.Columns = nil
		for _, visible := range profile.Presenter.Columns() {
			for i, column := range columns {
				if column.Name == visible.Name {
					column.Label = visible.Label
					profile.Columns = append(profile.Columns, column)
					columns = slices.Delete(columns, i, i+1)
					break
				}
			}
		}
		profile.Columns = append(profile.Columns, columns...)
	}
	for i := range profile.Columns {
		if profile.Columns[i].Name == "seq" {
			profile.Columns[i].Hidden = true
		}
	}
	return profile, nil
}
