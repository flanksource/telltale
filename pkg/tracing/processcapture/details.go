package processcapture

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/flanksource/telltale/pkg/tracing"
)

type Details struct {
	Identities []Identity   `json:"identities"`
	Metrics    []MetricRow  `json:"metrics"`
	Facets     []FacetEvent `json:"facets"`
}

func ReadDetails(ctx context.Context, runtime *tracing.Runtime, ref tracing.TraceRef, limit int64) (Details, error) {
	result := Details{Identities: []Identity{}, Metrics: []MetricRow{}, Facets: []FacetEvent{}}
	metric := tracing.TraceRef{Kind: MetricsTable, Stream: ref.Stream + "-metrics"}
	meta, err := runtime.Results.Backend.Meta(ctx, metric.Stream)
	if err != nil {
		return result, err
	}
	metric.AfterSeq, metric.ToSeq = max(0, meta.HighSeq-limit), meta.HighSeq
	if err := tracing.ReadRecords(ctx, runtime, ref, func(row Identity) error { result.Identities = append(result.Identities, row); return nil }); err != nil {
		return result, err
	}
	var tick int64
	if meta.HighSeq > 0 {
		if err := tracing.ReadRecords(ctx, runtime, metric, func(row MetricRow) error {
			result.Metrics = append(result.Metrics, row)
			tick = max(tick, row.Tick)
			return nil
		}); err != nil {
			return result, err
		}
	}
	held := map[[3]string]FacetEvent{}
	facets := tracing.TraceRef{Kind: FacetsTable, Stream: ref.Stream + "-facets"}
	err = tracing.ReadRecords(ctx, runtime, facets, func(row FacetEvent) error {
		if row.Tick > tick {
			return nil
		}
		key := [3]string{row.Process, row.Facet, row.Entity}
		switch row.Operation {
		case "removed":
			delete(held, key)
		case "added":
			held[key] = row
		default:
			return fmt.Errorf("unknown process facet operation %q", row.Operation)
		}
		return nil
	})
	for _, row := range held {
		result.Facets = append(result.Facets, row)
	}
	slices.SortFunc(result.Facets, func(a, b FacetEvent) int {
		return cmp.Or(cmp.Compare(a.Process, b.Process), cmp.Compare(a.Facet, b.Facet), cmp.Compare(a.Entity, b.Entity))
	})
	return result, err
}
