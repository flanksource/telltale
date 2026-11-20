package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
)

func profile(ctx context.Context, agent *agentclient.Agent, duration string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	window, err := time.ParseDuration(duration)
	if err != nil {
		return nil, err
	}
	session, err := agent.Arm(ctx, agentclient.ArmRequest{Probe: "jvm_jfr", Params: json.RawMessage(`{"engine":"jfr","settings":"profile","supportSnapshot":true}`), StopAt: time.Now().Add(window + 15*time.Second)})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err = errors.Join(err, session.Disarm(cleanup))
	}()
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	checkpoint, err := session.Checkpoint(ctx, json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	if len(checkpoint.Blobs) != 1 {
		return nil, fmt.Errorf("expected one JFR artifact, received %d", len(checkpoint.Blobs))
	}
	return scrub(ctx, checkpoint.Blobs[0].Data)
}
