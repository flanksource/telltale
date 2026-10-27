package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/telltale/sdk/protocol"
)

// ErrProbePanic identifies a panic in an extension callback.
var ErrProbePanic = errors.New("probe panic")

func panicError(value any) error { return fmt.Errorf("%w: %v", ErrProbePanic, value) }

func probeSpec(probe Probe) (spec protocol.ProbeSpec, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return probe.Spec(), nil
}

func opSpec(op Op) (spec protocol.OpSpec, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return op.Spec(), nil
}

func probeMutating(probe MutationProbe, params json.RawMessage) (mutating bool, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return probe.Mutating(params)
}

func armProbe(probe Probe, ctx context.Context, params json.RawMessage, sink Sink) (release func() error, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return probe.Arm(ctx, params, sink)
}

func checkpointProbe(probe Checkpointer, params json.RawMessage, sink Sink) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return probe.Checkpoint(context.Background(), params, sink)
}

func callOp(op Op, params json.RawMessage) (value any, blobs []protocol.Blob, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError(value)
		}
	}()
	return op.Call(context.Background(), params)
}
