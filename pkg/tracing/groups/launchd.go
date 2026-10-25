package groups

import (
	"context"
	"sync"
	"time"
)

// jobsEvery is how long a reading of launchd's jobs is reused: jobs start
// and stop far less often than a Top ticks.
const jobsEvery = 30 * time.Second

// launchd reads the host's running launchd jobs, at most once every
// jobsEvery; where there is no launchd it reads none.
type launchd struct {
	mu     sync.Mutex
	jobs   Jobs
	readAt time.Time
}

func (l *launchd) read(ctx context.Context) (Jobs, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jobs != nil && time.Since(l.readAt) < jobsEvery {
		return l.jobs, nil
	}
	jobs, err := readJobs(ctx)
	if err != nil {
		return nil, err
	}
	l.jobs, l.readAt = jobs, time.Now()
	return jobs, nil
}
