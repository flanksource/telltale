package spans

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/flanksource/telltale/pkg/agentclient"
	otlpspans "github.com/flanksource/telltale/pkg/otlp/spans"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	sdk "github.com/flanksource/telltale/sdk"
)

func decodeBlob(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[sdk.Span], error) {
	if blob.Info.ContentType != "application/json" {
		return seam.DecodedBlob[sdk.Span]{}, fmt.Errorf("span mirror requires application/json, got %q", blob.Info.ContentType)
	}
	var rows []struct {
		Native string `json:"native"`
	}
	if err := json.Unmarshal(blob.Data, &rows); err != nil {
		return seam.DecodedBlob[sdk.Span]{}, fmt.Errorf("decode span mirror: %w", err)
	}
	if rows == nil {
		return seam.DecodedBlob[sdk.Span]{}, fmt.Errorf("span mirror must be an array")
	}
	decoded := seam.DecodedBlob[sdk.Span]{Rows: make([]sdk.Span, 0, len(rows))}
	for _, row := range rows {
		span, err := otlpspans.DecodeNative(row.Native)
		if err != nil {
			return seam.DecodedBlob[sdk.Span]{}, err
		}
		merged := make(map[string]any, len(resource)+len(span.Resource))
		for key, value := range resource {
			merged[key] = value
		}
		maps.Copy(merged, span.Resource)
		span.Resource = merged
		decoded.Rows = append(decoded.Rows, span)
	}
	return decoded, nil
}
