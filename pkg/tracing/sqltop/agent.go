package sqltop

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type activeStoreKey struct{}

func agentArmParams(p Params) (json.RawMessage, error) {
	interval, err := p.pollInterval()
	if err != nil {
		return nil, err
	}
	minimum, err := p.minElapsed()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		PollIntervalMS int64    `json:"pollIntervalMs"`
		Limit          int      `json:"limit"`
		Databases      []string `json:"databases,omitempty"`
		Text           []string `json:"text,omitempty"`
		MinElapsed     string   `json:"minElapsed,omitempty"`
		MinElapsedNS   int64    `json:"minElapsedNs,omitempty"`
	}{interval.Milliseconds(), p.top(), p.Databases, p.Text, p.MinElapsed, int64(minimum)})
}

func (handler) AgentAvailable(probe string, selection target.Target) (bool, error) {
	return agentCapture(Params{Target: selection}).Available(probe, selection)
}

func agentCapture(p Params) seam.AgentSeam[Params, Sample] {
	return seam.AgentSeam[Params, Sample]{Probe: "sql_top", Target: func(p Params) target.Target { return p.Target }, ArmParams: agentArmParams,
		HandleBlob: func(ctx context.Context, blob agentclient.Blob, resource map[string]string, _ tracing.Emitter[Sample]) error {
			store, ok := ctx.Value(activeStoreKey{}).(*activeStore)
			if !ok {
				return fmt.Errorf("SQL top active store was not prepared")
			}
			if blob.Info.ContentType != profiles.ContentType {
				return fmt.Errorf("SQL top requires OTLP Profiles, got %q", blob.Info.ContentType)
			}
			source := resource["telltale.source.instance"]
			if source == "" {
				return fmt.Errorf("SQL top agent Hello is missing telltale.source.instance")
			}
			if blob.Info.Session == "" {
				return fmt.Errorf("SQL top agent blob is missing the authenticated capture session")
			}
			snapshots, err := profiles.Decode(blob.Data, profiles.Options{ExpectedKind: "sql_top", ExpectedCaptureID: blob.Info.Session, ExpectedSource: source})
			if err != nil {
				return err
			}
			for _, snapshot := range snapshots {
				rows, err := FromSnapshot(snapshot)
				if err != nil {
					return err
				}
				rows, err = filterActive(p.Options, rows)
				if err != nil {
					return err
				}
				frame := snapshot.Frame()
				if err := store.append(ctx, frame, rows, nil); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func filterActive(options Options, rows []ActiveSample) ([]ActiveSample, error) {
	minimum, err := options.minElapsed()
	if err != nil {
		return nil, err
	}
	kept := make([]ActiveSample, 0, len(rows))
	for _, row := range rows {
		if row.ElapsedMs < float64(minimum)/float64(time.Millisecond) {
			continue
		}
		if patterns := normalizePatterns(options.Databases); len(patterns) > 0 && !collections.MatchItems(row.Database, patterns...) {
			continue
		}
		if patterns := substringPatterns(options.Text); len(patterns) > 0 && !collections.MatchItems(row.SQL, patterns...) {
			continue
		}
		kept = append(kept, row)
	}
	slices.SortStableFunc(kept, func(a, b ActiveSample) int {
		return cmp.Or(cmp.Compare(b.ElapsedMs, a.ElapsedMs), cmp.Compare(a.OperationID, b.OperationID))
	})
	return kept[:min(len(kept), options.top())], nil
}
