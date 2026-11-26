package profiles

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/pprofile"
)

func decodeSamples(d pprofile.ProfilesDictionary, profile pprofile.Profile, snapshot Snapshot) ([]Row, error) {
	if profile.Samples().Len() == 0 {
		return nil, fmt.Errorf("top snapshots require an operation or snapshot control sample")
	}
	rows := make([]Row, 0, profile.Samples().Len())
	for _, sample := range profile.Samples().All() {
		attributes, err := readAttributes(d, sample.AttributeIndices())
		if err != nil {
			return nil, err
		}
		if kind, control := attributes[sampleKindAttribute]; control {
			if kind != snapshotControlKind || len(attributes) != 1 || profile.Samples().Len() != 1 || snapshot.Observed != 0 {
				return nil, fmt.Errorf("snapshot control requires its exact marker as the sole sample with zero observed operations")
			}
			if err := validateControl(sample, snapshot.CompletedAt); err != nil {
				return nil, err
			}
			return rows, nil
		}
		row, err := decodeRow(d, sample, attributes)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// WORKAROUND(collector-empty-profiles): Validate and retain the transport control sample without projecting a fabricated active operation.
// Correct fix: Collector must preserve metadata-only profiles without a control sample.
// Ref: https://github.com/open-telemetry/opentelemetry-collector/blob/v0.165.0/receiver/otlpreceiver/internal/profiles/otlp.go#L36-L38; approved by user 2026-11-15.
func validateControl(sample pprofile.Sample, observedAt time.Time) error {
	if sample.Values().Len() != 1 || sample.Values().At(0) != 0 || sample.TimestampsUnixNano().Len() != 1 || sample.TimestampsUnixNano().At(0) != uint64(observedAt.UnixNano()) || sample.StackIndex() != 0 || sample.LinkIndex() != 0 {
		return fmt.Errorf("snapshot control requires a zero elapsed value, the snapshot observation timestamp and zero stack/link references")
	}
	return nil
}
