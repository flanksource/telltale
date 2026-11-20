package processcapture

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/profilestore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
)

type ProcessRow struct {
	Key           string     `json:"key"`
	PID           int        `json:"pid"`
	Name          string     `json:"name"`
	Command       string     `json:"command"`
	ExitedAt      *time.Time `json:"exitedAt,omitempty"`
	SampledAt     *time.Time `json:"sampledAt,omitempty"`
	CPUPercent    *float64   `json:"cpuPercent"`
	RSSBytes      *uint64    `json:"rssBytes"`
	MemoryPercent *float64   `json:"memoryPercent"`
}

func LatestProcesses(identities []Identity, metrics []MetricRow) ([]ProcessRow, error) {
	rows := make([]ProcessRow, len(identities))
	index := map[string]int{}
	ticks := map[string]int64{}
	for i, p := range identities {
		if p.Key == "" || p.PID <= 0 {
			return nil, fmt.Errorf("process identity requires key and pid")
		}
		if _, exists := index[p.Key]; exists {
			return nil, fmt.Errorf("duplicate process identity %s", p.Key)
		}
		index[p.Key] = i
		rows[i] = ProcessRow{Key: p.Key, PID: p.PID, Name: p.Name, Command: p.Command, ExitedAt: p.ExitedAt}
	}
	for _, sample := range metrics {
		i, ok := index[sample.Entity]
		if !ok {
			return nil, fmt.Errorf("metrics reference unknown process %q", sample.Entity)
		}
		row := &rows[i]
		if row.SampledAt == nil || sample.Tick >= ticks[sample.Entity] {
			row.SampledAt, row.CPUPercent, row.RSSBytes, row.MemoryPercent = &sample.At, sample.CPUPercent, &sample.RSSBytes, &sample.MemoryPercent
			ticks[sample.Entity] = sample.Tick
		}
	}
	slices.SortFunc(rows, func(a, b ProcessRow) int {
		if compared := cmp.Compare(a.PID, b.PID); compared != 0 {
			return compared
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return rows, nil
}

func latestMetricsView() recordresults.ResultView {
	return recordresults.ResultView{Name: "latest", Title: "Latest process measurements",
		Query: `SELECT entity, observedAt, tick, cpuPercent, rssBytes, memoryPercent, MAX(seq) AS seq FROM stream_rows GROUP BY entity`,
		Columns: []query.ColumnDef{
			{Name: "entity", Type: query.ColumnTypeString}, {Name: "observedAt", Type: query.ColumnTypeDateTime},
			{Name: "tick", Type: query.ColumnTypeNumber}, {Name: "cpuPercent", Type: query.ColumnTypeNumber},
			{Name: "rssBytes", Type: query.ColumnTypeNumber}, {Name: "memoryPercent", Type: query.ColumnTypeNumber}, {Name: "seq", Type: query.ColumnTypeNumber},
		}, Order: query.Order{{Column: "entity", Unique: true}}}
}

func ReadLatest(ctx context.Context, runtime *tracing.Runtime, ref tracing.TraceRef) ([]ProcessRow, error) {
	if ref.Kind != Kind {
		return nil, fmt.Errorf("process snapshot requires a process capture")
	}
	meta, err := runtime.Results.Backend.Meta(ctx, ref.Stream+"-metrics")
	if err != nil {
		return nil, err
	}
	var identities []Identity
	if err := tracing.ReadRecords(ctx, runtime, tracing.TraceRef{Kind: Kind, Stream: ref.Stream}, func(p Identity) error {
		identities = append(identities, p)
		return nil
	}); err != nil {
		return nil, err
	}
	if meta.HighSeq == 0 {
		return LatestProcesses(identities, nil)
	}
	profile, err := runtime.Results.Registry.Get(ctx, "traces/"+MetricsTable+"/latest")
	if err != nil {
		return nil, err
	}
	params := (tracing.TraceRef{Kind: MetricsTable, Stream: ref.Stream + "-metrics", ToSeq: meta.HighSeq}).Params()
	release, err := runtime.Results.Registry.BeforeExecute(ctx, []profilestore.ReadRequest{{Profile: profile, Params: params}})
	if err != nil {
		return nil, err
	}
	defer release()
	dbctx := dbcontext.NewContext(ctx).WithConnectionResolver(runtime.Results.Registry.ResolveConnection)
	var metrics []MetricRow
	for row, err := range query.Rows(query.ExecutePages(dbctx, profile, query.PageRequest{Limit: 500, Strategy: query.PagingCursor}, params)) {
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		var sample MetricRow
		if err := json.Unmarshal(data, &sample); err != nil {
			return nil, err
		}
		metrics = append(metrics, sample)
	}
	return LatestProcesses(identities, metrics)
}
