package sql

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
)

func decodeAgentStatement(raw json.RawMessage) (StatementRow, error) {
	var record struct {
		connection.Statement
		Observation
		DurationNS *int64 `json:"durationNs"`
		Rows       *int64 `json:"rows"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return StatementRow{}, err
	}
	if record.API != "" {
		if record.API != "jdbc" && record.API != "r2dbc" {
			return StatementRow{}, fmt.Errorf("unsupported SQL API %q", record.API)
		}
		if record.OperationID == "" || record.StartedAt.IsZero() || record.DurationNS == nil || *record.DurationNS < 0 {
			return StatementRow{}, fmt.Errorf("JVM SQL requires operationId, startedAt and non-negative durationNs")
		}
		if strings.TrimSpace(record.SQL) == "" && record.SQLUnavailableReason == "" {
			return StatementRow{}, fmt.Errorf("JVM SQL requires SQL text or sqlUnavailableReason")
		}
		if record.SQLUnavailableReason != "" && record.SQL != "" {
			return StatementRow{}, fmt.Errorf("unavailable JVM SQL must not contain invented SQL text")
		}
		record.Duration = time.Duration(*record.DurationNS)
		record.Statement.Rows = -1
	}
	if record.Rows != nil {
		record.Statement.Rows = *record.Rows
	}
	row := FromStatement(record.Statement)
	row.Observation = record.Observation
	if row.SQLUnavailableReason != "" {
		row.RepeatKey = ""
	}
	return row, nil
}

func agentArmParams(p Params) (json.RawMessage, error) {
	minimum, err := p.minimum()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Params
		MinDurationNS int64 `json:"minDurationNs"`
	}{p, int64(minimum)})
}

func decodeAgentBlob(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[StatementRow], error) {
	if blob.Info.ContentType != "application/json" {
		return seam.DecodedBlob[StatementRow]{}, fmt.Errorf("SQL observations require application/json, received %q", blob.Info.ContentType)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(blob.Data, &raw); err != nil {
		return seam.DecodedBlob[StatementRow]{}, fmt.Errorf("decode SQL observation batch: %w", err)
	}
	if raw == nil {
		return seam.DecodedBlob[StatementRow]{}, fmt.Errorf("SQL observation batch must be an array")
	}
	rows := make([]StatementRow, 0, len(raw))
	for index, item := range raw {
		row, err := decodeAgentStatement(item)
		if err != nil {
			return seam.DecodedBlob[StatementRow]{}, fmt.Errorf("decode SQL observation %d: %w", index+1, err)
		}
		if row.API == "" {
			return seam.DecodedBlob[StatementRow]{}, fmt.Errorf("SQL observation %d requires a JVM SQL API", index+1)
		}
		row.Resource = resource
		rows = append(rows, row)
	}
	return seam.DecodedBlob[StatementRow]{Rows: rows}, nil
}
