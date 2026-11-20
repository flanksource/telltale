package jvmruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/api"
)

// ThreadsOp samples every live thread's state and CPU over a short window.
const ThreadsOp = "jvm.threads"

// ThreadSample is the agent's jvm.threads: every live thread, with CPU % over
// SampledMs when the JVM has thread CPU timing on.
type ThreadSample struct {
	SampledMs      int      `json:"sampledMs"`
	CPUTimeEnabled bool     `json:"cpuTimeEnabled"`
	Deadlocked     []int64  `json:"deadlocked"`
	Threads        []Thread `json:"threads"`
}

// Thread is one thread's row. The pointer fields are null when the JVM does
// not measure them (CPU timing or contention monitoring off), never 0.
type Thread struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	State         string   `json:"state"`
	Daemon        bool     `json:"daemon"`
	Priority      int      `json:"priority"`
	CPUPercent    *float64 `json:"cpuPercent"`
	CPUTimeMs     *int64   `json:"cpuTimeMs"`
	BlockedCount  int64    `json:"blockedCount"`
	BlockedTimeMs *int64   `json:"blockedTimeMs"`
	WaitedCount   int64    `json:"waitedCount"`
	WaitedTimeMs  *int64   `json:"waitedTimeMs"`
	LockName      *string  `json:"lockName"`
	LockOwnerID   *int64   `json:"lockOwnerId"`
	LockOwnerName *string  `json:"lockOwnerName"`
	InNative      bool     `json:"inNative"`
	Suspended     bool     `json:"suspended"`
}

// DecodeThreads decodes the agent's sample, refusing one with no threads: a
// live JVM always has at least the thread answering.
func DecodeThreads(raw json.RawMessage) (ThreadSample, error) {
	sample, err := decodeObject[ThreadSample](ThreadsOp, raw, "sampledMs", "cpuTimeEnabled", "deadlocked", "threads")
	if err == nil && (len(sample.Threads) == 0 || sample.SampledMs <= 0) {
		err = fmt.Errorf("%s returned no thread sample: %s", ThreadsOp, raw)
	}
	return sample, err
}

func stateClass(state string) string {
	switch state {
	case "RUNNABLE":
		return "text-green-600"
	case "BLOCKED":
		return "text-red-600"
	default:
		return "text-muted"
	}
}

func (Thread) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("id").Label("ID").Build(),
		api.Column("name").Label("Name").MaxWidth(60).Build(),
		api.Column("state").Label("State").Build(),
		api.Column("cpu").Label("CPU").Build(),
		api.Column("daemon").Label("Daemon").Build(),
	}
}

func (t Thread) Row() map[string]any {
	cpu := ""
	if t.CPUPercent != nil {
		cpu = fmt.Sprintf("%.1f%%", *t.CPUPercent)
	}
	return map[string]any{
		"id":     t.ID,
		"name":   t.Name,
		"state":  api.Text{}.Append(t.State, stateClass(t.State)),
		"cpu":    cpu,
		"daemon": t.Daemon,
	}
}

// Pretty is the sample's heading, with any deadlock in red, over its Thread table.
func (s ThreadSample) Pretty() api.Text {
	text := api.Text{}.Append("Threads", "font-bold").Append(fmt.Sprintf(" %d threads · sampled %dms", len(s.Threads), s.SampledMs), "text-muted")
	if !s.CPUTimeEnabled {
		text = text.Append(" · CPU time off", "text-yellow-600")
	}
	if len(s.Deadlocked) > 0 {
		ids := make([]string, len(s.Deadlocked))
		for i, id := range s.Deadlocked {
			ids[i] = fmt.Sprint(id)
		}
		text = text.Append(" · DEADLOCK threads "+strings.Join(ids, ", "), "text-red-600")
	}
	return text.NewLine().Add(api.NewTableFrom(s.Threads))
}
