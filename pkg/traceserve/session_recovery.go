package traceserve

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/owner"
	"github.com/flanksource/telltale/pkg/tracing"
)

// Recover reconciles a reopened run when it owns the SQLite lock. If it opens
// as a reader, the same check runs on session requests after a later takeover.
func Recover(runtime *tracing.Runtime) error {
	var mu sync.Mutex
	var recovered bool
	runtime.RecoverSessions = func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if recovered || runtime.Results.Role() != owner.RoleOwner {
			return nil
		}
		if err := recoverSessions(ctx, runtime); err != nil {
			return err
		}
		recovered = true
		return nil
	}
	return runtime.RecoverSessions(context.Background())
}

func recoverSessions(ctx context.Context, runtime *tracing.Runtime) error {
	page, err := runtime.SessionStore.List(ctx, query.SessionFilter{
		State: []string{string(query.SessionStarting), string(query.SessionRunning), string(query.SessionStopping)},
	})
	if err != nil {
		return fmt.Errorf("recover run sessions: %w", err)
	}
	now := time.Now()
	current := runtime.Sessions.Owner()
	var errs []error
	for _, record := range page.Items {
		if record.State.Terminal() || record.Owner.Boot == current.Boot {
			continue
		}
		// The run's namespace and SQLite lock identify its former writer
		// conclusively, even inside the normal heartbeat grace period.
		status := record.SessionStatus
		status.State, status.StopReason = query.SessionInterrupted, query.StopReasonOwnerRestart
		status.StoppedAt, status.UpdatedAt = &now, now
		if err := runtime.SessionStore.Update(ctx, record.ID, status); err != nil {
			errs = append(errs, fmt.Errorf("recover run session %s: %w", record.ID, err))
		}
	}
	return errors.Join(errs...)
}
