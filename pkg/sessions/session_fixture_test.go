package sessions

import (
	"context"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/clicky/cache"
	"github.com/flanksource/commons-db/query"
)

// contractEpoch anchors the contract fixture an hour back, well inside every
// store's retention window, so window filters read as minute offsets.
func contractEpoch() time.Time { return time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC() }

func contractRecord(epoch time.Time, id, profile string, state query.SessionState, startedMin int, events int64, labels map[string]string) query.SessionRecord {
	started := epoch.Add(time.Duration(startedMin) * time.Minute)
	return query.SessionRecord{
		SessionStart: query.SessionStart{
			SchemaVersion: query.SessionSchemaVersion, ID: id, Profile: profile, Kind: query.KindCapture,
			Role: query.SessionRoleCapture, Labels: labels, Principal: "admin",
			Params: map[string]any{"class": "com.example.Listener"},
			Owner:  query.SessionOwner{Host: "pod-a", PID: 1, Boot: "boot-a"}, StartedAt: started,
		},
		SessionStatus: query.SessionStatus{State: state, EventCount: events, UpdatedAt: started, HeartbeatAt: started},
	}
}

// contractFixture mirrors the query package's filter fixture: b and c start at
// the same minute so a startedAt sort needs the id tie-breaker, and d has no
// labels at all.
func contractFixture(epoch time.Time) []query.SessionRecord {
	restarted := contractRecord(epoch, "f", "trace-capture/jvm_trace", query.SessionRunning, 5, 1, map[string]string{"target": "cycle", "origin": "web"})
	restarted.RestartOf = "c"
	restarted.Principal = "operator"
	return []query.SessionRecord{
		contractRecord(epoch, "c", "trace-capture/jvm_trace", query.SessionStopped, 2, 30, map[string]string{"target": "cycle", "origin": "web"}),
		contractRecord(epoch, "a", "trace-capture/jvm_trace", query.SessionRunning, 0, 10, map[string]string{"target": "example", "origin": "testplan"}),
		contractRecord(epoch, "b", "trace-capture/sql_xevent", query.SessionCompleted, 2, 20, map[string]string{"target": "cycle", "origin": "web"}),
		contractRecord(epoch, "d", "trace-capture/jvm_trace", query.SessionFailed, 3, 5, nil),
		restarted,
	}
}

// countingCache counts how often each session's start key is read, so a spec
// can see which records a request read and how many times.
type countingCache struct {
	cache.Store
	mu    sync.Mutex
	reads map[string]int
}

func (c *countingCache) MGet(ctx context.Context, keys iter.Seq[string]) iter.Seq2[cache.Entry, error] {
	c.mu.Lock()
	for key := range keys {
		if strings.HasSuffix(key, ":start") {
			c.reads[key]++
		}
	}
	c.mu.Unlock()
	return c.Store.MGet(ctx, keys)
}

func (c *countingCache) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	if strings.HasSuffix(key, ":start") {
		c.reads[key]++
	}
	c.mu.Unlock()
	return c.Store.Get(ctx, key)
}

// readsOf is how often session id's start key was read under any prefix.
func (c *countingCache) readsOf(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for key, reads := range c.reads {
		if strings.HasSuffix(key, "sessions:v1:"+id+":start") {
			total += reads
		}
	}
	return total
}

func (c *countingCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = map[string]int{}
}
