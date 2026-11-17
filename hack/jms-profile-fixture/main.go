// Emit a deterministic-shape JMS handler profile for local viewer smoke checks.
package main

import (
	"fmt"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/google/uuid"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: go run ./hack/jms-profile-fixture <output.pb>")
	}
	now := time.Now()
	snapshot := profiles.Snapshot{Kind: "jms_top", CaptureID: "handler-fixture", Source: "fixture-jvm", ProfileID: uuid.NewString(), Sequence: 1, Complete: true, Observed: 2, Limit: 50, StartedAt: now, CompletedAt: now, Resource: map[string]any{"service.name": "jms-viewer-fixture"}, Rows: []profiles.Row{
		{OperationID: "orders-handler", ElapsedNS: int64(2 * time.Second), SampledAt: now, Attributes: map[string]any{"destination": "orders", "handler": "OrderListener.accept", "state": "busy", "activeCount": 2}},
		{OperationID: "replies-handler", SampledAt: now, Attributes: map[string]any{"destination": "replies", "handler": "ReplyListener.accept", "state": "idle", "activeCount": 0}},
	}}
	data, err := profiles.Encode([]profiles.Snapshot{snapshot})
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], data, 0600); err != nil {
		panic(err)
	}
	fmt.Println(os.Args[1])
}
