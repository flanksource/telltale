package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

type topFrame struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	Tick        int64          `json:"tick"`
	CompletedAt time.Time      `json:"completedAt"`
	Summary     map[string]any `json:"summary"`
}

type topSnapshot struct {
	Frame topFrame         `json:"frame"`
	Rows  []map[string]any `json:"rows"`
	Count int              `json:"count"`
}

func decodeSnapshot(top topSnapshot) (profiles.Snapshot, error) {
	native, ok := top.Frame.Summary["otlp"].(string)
	if !ok || native == "" {
		return profiles.Snapshot{}, fmt.Errorf("top frame %s has no native Profiles evidence", top.Frame.ID)
	}
	data, err := base64.StdEncoding.DecodeString(native)
	if err != nil {
		return profiles.Snapshot{}, fmt.Errorf("decode native Profiles base64: %w", err)
	}
	snapshots, err := profiles.Decode(data, profiles.Options{})
	if err != nil {
		return profiles.Snapshot{}, fmt.Errorf("decode native Profiles: %w", err)
	}
	if len(snapshots) != 1 {
		return profiles.Snapshot{}, fmt.Errorf("expected one native snapshot, received %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Sequence != top.Frame.Tick || snapshot.ProfileID != top.Frame.ID {
		return profiles.Snapshot{}, fmt.Errorf("projected frame identity differs from native Profiles")
	}
	return snapshot, nil
}

func checkActive(kind string, top topSnapshot) error {
	snapshot, err := decodeSnapshot(top)
	if err != nil {
		return err
	}
	if snapshot.Kind != kind || !snapshot.Complete {
		return fmt.Errorf("expected complete %s snapshot, received %s complete=%t", kind, snapshot.Kind, snapshot.Complete)
	}
	if len(top.Rows) != len(snapshot.Rows) || top.Count != len(snapshot.Rows) {
		return fmt.Errorf("native and projected operation counts differ: native=%d rows=%d count=%d", len(snapshot.Rows), len(top.Rows), top.Count)
	}
	field, values := "direction", []string{"incoming", "outgoing"}
	if kind == "sql_top" {
		field, values = "api", []string{"jdbc", "r2dbc"}
	}
	for _, value := range values {
		if !hasOperation(snapshot.Rows, field, value) {
			return fmt.Errorf("%s has no active demo %s=%s operation", kind, field, value)
		}
	}
	return nil
}

func hasOperation(rows []profiles.Row, field, value string) bool {
	for _, row := range rows {
		if row.Attributes[field] != value || row.OperationID == "" || row.ElapsedNS <= 0 {
			continue
		}
		if field == "api" && strings.Contains(strings.ToUpper(fmt.Sprint(row.Attributes["sql"])), "DEMO_HOLD") {
			return true
		}
		if field == "direction" && strings.Contains(fmt.Sprint(row.Attributes["url"]), "/demo/") {
			if value == "outgoing" || strings.Contains(fmt.Sprint(row.Attributes["route"]), "/demo/{") {
				return true
			}
		}
	}
	return false
}

func checkUpdated(previous, current topSnapshot) error {
	before, err := decodeSnapshot(previous)
	if err != nil {
		return err
	}
	after, err := decodeSnapshot(current)
	if err != nil {
		return err
	}
	if before.Kind != after.Kind || before.CaptureID != after.CaptureID || before.Source != after.Source {
		return fmt.Errorf("snapshot capture identity changed while observing held operations")
	}
	if after.Sequence <= before.Sequence {
		return fmt.Errorf("snapshot sequence has not advanced: previous=%d current=%d", before.Sequence, after.Sequence)
	}
	currentRows := make(map[string]profiles.Row, len(after.Rows))
	for _, row := range after.Rows {
		currentRows[row.OperationID] = row
	}
	for _, row := range before.Rows {
		updated, ok := currentRows[row.OperationID]
		if !ok {
			return fmt.Errorf("held operation %s disappeared", row.OperationID)
		}
		if updated.ElapsedNS <= row.ElapsedNS {
			return fmt.Errorf("operation %s elapsed did not increase: previous=%d current=%d", row.OperationID, row.ElapsedNS, updated.ElapsedNS)
		}
	}
	return nil
}

func checkCleared(top topSnapshot) error {
	snapshot, err := decodeSnapshot(top)
	if err != nil {
		return err
	}
	if len(snapshot.Rows) != 0 || snapshot.Observed != 0 || !snapshot.Complete {
		return fmt.Errorf("native snapshot is not a complete empty clear: retained=%d observed=%d complete=%t", len(snapshot.Rows), snapshot.Observed, snapshot.Complete)
	}
	if len(top.Rows) != 0 || top.Count != 0 {
		return fmt.Errorf("empty snapshot contains projected operation rows: rows=%d count=%d", len(top.Rows), top.Count)
	}
	native, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(snapshot.Native)
	if err != nil {
		return fmt.Errorf("read native clear sample: %w", err)
	}
	if native.SampleCount() != 1 {
		return fmt.Errorf("schema-v2 clear requires one control sample, received %d", native.SampleCount())
	}
	return nil
}
