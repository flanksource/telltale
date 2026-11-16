package httptop

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing/seam"
)

func Decode(blob agentclient.Blob, resource map[string]string) (seam.DecodedBlob[Sample], error) {
	if blob.Info.ContentType != profiles.ContentType {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("HTTP top requires OTLP Profiles, received %q", blob.Info.ContentType)
	}
	if blob.Info.Session == "" {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("HTTP top agent blob is missing the authenticated capture session")
	}
	source := resource["telltale.source.instance"]
	if source == "" {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("HTTP top agent Hello is missing telltale.source.instance")
	}
	snapshots, err := profiles.Decode(blob.Data, profiles.Options{ExpectedKind: Kind, ExpectedCaptureID: blob.Info.Session, ExpectedSource: source})
	if err != nil {
		return seam.DecodedBlob[Sample]{}, err
	}
	if len(snapshots) != 1 {
		return seam.DecodedBlob[Sample]{}, fmt.Errorf("HTTP top requires one snapshot per blob, received %d", len(snapshots))
	}
	snapshot := snapshots[0]
	rows, err := FromSnapshot(snapshot)
	if err != nil {
		return seam.DecodedBlob[Sample]{}, err
	}
	for i := range rows {
		for name, value := range resource {
			rows[i].Resource[name] = value
		}
	}
	frame := snapshot.Frame()
	return seam.DecodedBlob[Sample]{Rows: rows, Frame: &frame}, nil
}

func FromSnapshot(snapshot profiles.Snapshot) ([]Sample, error) {
	if snapshot.Kind != Kind {
		return nil, fmt.Errorf("HTTP top cannot decode snapshot kind %q", snapshot.Kind)
	}
	frame := snapshot.Frame()
	rows := make([]Sample, 0, len(snapshot.Rows))
	for _, native := range snapshot.Rows {
		attributes, err := json.Marshal(native.Attributes)
		if err != nil {
			return nil, err
		}
		var sample Sample
		if err := json.Unmarshal(attributes, &sample); err != nil {
			return nil, fmt.Errorf("HTTP top attributes: %w", err)
		}
		if sample.StartedAt.IsZero() {
			return nil, fmt.Errorf("HTTP top operation %q has no startedAt", native.OperationID)
		}
		sample.RequestID = native.OperationID
		sample.Source = frame.Source
		sample.ElapsedMs = float64(native.ElapsedNS) / float64(1e6)
		sample.SampledAt = native.SampledAt
		sample.TraceID, sample.SpanID = native.TraceID, native.SpanID
		sample.Resource = map[string]string{}
		for name, value := range snapshot.Resource {
			sample.Resource[name] = fmt.Sprint(value)
		}
		rows = append(rows, sample)
	}
	return rows, nil
}
