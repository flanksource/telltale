// Package profiles encodes Telltale active-operation snapshots as OTLP Profiles.
package profiles

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
	sdkprofiles "github.com/flanksource/telltale/sdk/profiles"
)

const ContentType = sdkprofiles.ContentType
const DevelopmentVersion = sdkprofiles.DevelopmentVersion
const Scope = sdkprofiles.Scope
const conventionVersion = sdkprofiles.ConventionVersion
const sampleKindAttribute = sdkprofiles.SampleKindAttribute
const snapshotControlKind = sdkprofiles.SnapshotControlKind

var ErrLimit = errors.New("OTLP Profiles limit exceeded")

type Options struct {
	MaxBytes, MaxProfiles, MaxSamples               int
	ExpectedKind, ExpectedCaptureID, ExpectedSource string
}

type Row = sdkprofiles.Row

type Snapshot sdkprofiles.Snapshot

func Encode(snapshots []Snapshot) ([]byte, error) {
	wire := make([]sdkprofiles.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		wire = append(wire, sdkprofiles.Snapshot(snapshot))
	}
	return sdkprofiles.Encode(wire, tracing.MaskAttribute)
}

func (s Snapshot) Frame() sampleframe.Frame {
	frame := sampleframe.Frame{Source: s.Source + "/" + s.CaptureID, Tick: s.Sequence, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt,
		Observed: s.Observed, Limit: s.Limit, Summary: map[string]any{"profileId": s.ProfileID, "captureId": s.CaptureID, "checkpoint": s.Checkpoint,
			"receivedAt": time.Now().UTC().Format(time.RFC3339Nano), "dropped": s.Dropped, "complete": s.Complete, "otlp": base64.StdEncoding.EncodeToString(s.Native), "digest": s.Digest, "profilesDevelopmentVersion": DevelopmentVersion}}
	if s.PollIntervalMS > 0 {
		frame.Summary["pollIntervalMs"] = s.PollIntervalMS
	}
	return frame
}
