package jvmtrace

import (
	"context"
	"encoding/json"

	"github.com/flanksource/telltale/pkg/tracing"
)

type CheckpointParams struct {
	Reason string `json:"reason,omitempty"`
}

func checkpointParams(ctx context.Context) (json.RawMessage, error) {
	var params CheckpointParams
	if _, err := tracing.DecodeCheckpointParams(ctx, &params); err != nil {
		return nil, err
	}
	return json.Marshal(params)
}
