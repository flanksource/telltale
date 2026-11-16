package sqltop

import (
	"encoding/json"
	"fmt"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

func FromSnapshot(snapshot profiles.Snapshot) ([]ActiveSample, error) {
	if snapshot.Kind != "sql_top" {
		return nil, fmt.Errorf("SQL top cannot project profile kind %q", snapshot.Kind)
	}
	rows := make([]ActiveSample, len(snapshot.Rows))
	for i, observation := range snapshot.Rows {
		data, err := json.Marshal(observation.Attributes)
		if err != nil {
			return nil, err
		}
		var row ActiveSample
		if err := json.Unmarshal(data, &row); err != nil {
			return nil, fmt.Errorf("decode SQL profile attributes: %w", err)
		}
		if row.StartedAt.IsZero() || (row.API != "jdbc" && row.API != "r2dbc") || (row.SQL == "" && row.SQLUnavailableReason == "") {
			return nil, fmt.Errorf("SQL profile requires startedAt, jdbc/r2dbc api and SQL text or unavailable reason")
		}
		if row.SQL != "" && row.SQLUnavailableReason != "" {
			return nil, fmt.Errorf("unavailable SQL profile must not contain invented SQL text")
		}
		row.Source, row.OperationID, row.SampledAt = snapshot.Frame().Source, observation.OperationID, observation.SampledAt
		row.ElapsedMs = float64(observation.ElapsedNS) / float64(time.Millisecond)
		row.TraceID, row.SpanID = observation.TraceID, observation.SpanID
		rows[i] = row
	}
	return rows, nil
}

type activeReader struct{}

func (activeReader) Params() Params                                 { return Params{Target: target.Target{Seam: target.Agent}} }
func (activeReader) Schema() recordresults.ResultType[ActiveSample] { return ActiveResult() }
func (activeReader) Handle(dbcontext.Context, Params, tracing.Emitter[ActiveSample], tracing.Records[ActiveSample]) error {
	return fmt.Errorf("received SQL active results cannot start captures")
}

func NewActiveKind() *tracing.Handler[Params, ActiveSample] {
	spec := topSpec()
	spec.Result = ""
	return tracing.NewHandler[Params, ActiveSample](activeReader{}, tracing.Capabilities{Follow: true}, target.Agent).WithTop(spec)
}
