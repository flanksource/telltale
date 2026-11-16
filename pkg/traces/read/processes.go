package read

import (
	"context"

	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

type ProcessRow struct {
	processcapture.ProcessRow
	RunID  string `json:"runId"`
	Stream string `json:"stream"`
}

type ProcessSnapshot struct {
	Armed bool         `json:"armed"`
	Rows  []ProcessRow `json:"rows"`
}

func Processes(ctx context.Context, refs []Ref) (ProcessSnapshot, error) {
	result := ProcessSnapshot{Rows: []ProcessRow{}}
	for _, ref := range refs {
		if ref.Kind != processcapture.Kind {
			continue
		}
		result.Armed = true
		rows, err := processcapture.ReadLatest(ctx, ref.Runtime, ref.TraceRef)
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			result.Rows = append(result.Rows, ProcessRow{ProcessRow: row, RunID: ref.RunID, Stream: ref.Stream})
		}
	}
	return result, nil
}
