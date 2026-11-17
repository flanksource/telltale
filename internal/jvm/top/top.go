// Package top samples one Java runtime for terminal and web dashboards.
package top

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

type Frame struct {
	AgentID   string                  `json:"agentId"`
	SampledAt time.Time               `json:"sampledAt"`
	Metrics   jvmruntime.Metrics      `json:"metrics"`
	Threads   jvmruntime.ThreadSample `json:"threads"`
}

type Caller func(context.Context, agentclient.CallRequest) (json.RawMessage, error)

func Sample(ctx context.Context, id string, call Caller) (Frame, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	frame := Frame{AgentID: id}
	raw, err := call(ctx, agentclient.CallRequest{Op: jvmruntime.MetricsOp, Params: json.RawMessage(`{}`)})
	if err != nil {
		return frame, fmt.Errorf("JVM metrics: %w", err)
	}
	if frame.Metrics, err = jvmruntime.DecodeMetrics(raw); err != nil {
		return frame, err
	}
	raw, err = call(ctx, agentclient.CallRequest{Op: jvmruntime.ThreadsOp, Params: json.RawMessage(`{"sampleMs":200}`)})
	if err != nil {
		return frame, fmt.Errorf("JVM threads: %w", err)
	}
	if frame.Threads, err = jvmruntime.DecodeThreads(raw); err != nil {
		return frame, err
	}
	frame.SampledAt = time.Now().UTC()
	return frame, nil
}

func Order(rows []jvmruntime.Thread, by string, reverse bool, query string) []jvmruntime.Thread {
	filtered := make([]jvmruntime.Thread, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(fmt.Sprintf("%d %s %s", row.ID, row.Name, row.State)), strings.ToLower(query)) {
			filtered = append(filtered, row)
		}
	}
	slices.SortFunc(filtered, func(a, b jvmruntime.Thread) int {
		var order int
		switch by {
		case "name":
			order = cmp.Compare(a.Name, b.Name)
		case "state":
			order = cmp.Compare(a.State, b.State)
		case "id":
			order = cmp.Compare(a.ID, b.ID)
		default:
			if a.CPUPercent == nil && b.CPUPercent != nil {
				return 1
			}
			if a.CPUPercent != nil && b.CPUPercent == nil {
				return -1
			}
			if a.CPUPercent != nil && b.CPUPercent != nil {
				order = cmp.Compare(*b.CPUPercent, *a.CPUPercent)
			}
		}
		if reverse {
			order = -order
		}
		if order == 0 {
			return cmp.Compare(a.ID, b.ID)
		}
		return order
	})
	return filtered
}

func (f Frame) Summary() string {
	cpu := "unavailable"
	if f.Metrics.ProcessCPUPercent != nil {
		cpu = fmt.Sprintf("%.1f%%", *f.Metrics.ProcessCPUPercent)
	}
	out := fmt.Sprintf("JVM Top · %s · Java %s · uptime %s\nCPU %s of capacity · %d threads · sampled %s", f.AgentID, f.Metrics.Runtime.JavaVersion, (time.Duration(f.Metrics.Runtime.Uptime) * time.Millisecond).Round(time.Second), cpu, len(f.Threads.Threads), f.SampledAt.Format(time.TimeOnly))
	for _, area := range []struct {
		name  string
		pools []jvmruntime.MemoryPool
	}{{"Heap", f.Metrics.Memory.Heap}, {"Non-heap", f.Metrics.Memory.NonHeap}, {"Buffers", f.Metrics.Memory.Buffer}} {
		var used, committed int64
		for _, pool := range area.pools {
			used += pool.Used
			committed += pool.Committed
		}
		out += fmt.Sprintf("\n%s %s used / %s committed", area.name, api.HumanizeBytes(used), api.HumanizeBytes(committed))
	}
	for _, gc := range f.Metrics.GarbageCollectors {
		out += fmt.Sprintf("\nGC %s: %d collections / %dms (lifetime)", gc.Name, gc.CollectionCount, gc.CollectionTime)
	}
	states := map[string]int{}
	for _, thread := range f.Threads.Threads {
		states[thread.State]++
	}
	out += "\nThreads"
	for _, state := range []string{"RUNNABLE", "BLOCKED", "WAITING", "TIMED_WAITING"} {
		out += fmt.Sprintf(" · %s %d", state, states[state])
	}
	if !f.Threads.CPUTimeEnabled {
		out += "\nThread CPU unavailable: JVM CPU timing disabled"
	}
	if len(f.Threads.Deadlocked) > 0 {
		out += fmt.Sprintf("\nDEADLOCK: threads %v", f.Threads.Deadlocked)
	}
	return out
}

func (f Frame) Pretty() api.Text {
	return api.Text{}.Append(f.Summary()).NewLine().Add(Table(Order(f.Threads.Threads, "cpu", false, "")))
}
