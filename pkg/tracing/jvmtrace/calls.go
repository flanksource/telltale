package jvmtrace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

// Calls folds a stream's method events into calls, one line each: a call is
// complete at its exit, and an outermost call prints with the calls nested
// inside it, so a traced call reads as one tree. It keeps the run's stats.
type Calls struct {
	// Location formats each call's start; the local zone when nil.
	Location *time.Location
	open     map[frameKey]*call
	stats    Stats
}

type frameKey struct{ thread, frame int64 }

type call struct {
	event    Event
	value    json.RawMessage
	err      string
	duration time.Duration
	children []*call
	nested   bool
}

// Stats are what the calls a run folded add up to.
type Stats struct {
	Calls   int
	Threw   int
	Dropped int64
	Slowest string
	Longest time.Duration
}

// Add folds one event, returning the lines it completes.
func (c *Calls) Add(event Event) []api.Text {
	if c.open == nil {
		c.open = map[frameKey]*call{}
	}
	key := frameKey{event.ThreadID, event.Frame}
	switch event.Kind {
	case "dropped":
		c.stats.Dropped += event.Dropped
		return []api.Text{api.Text{}.Append(c.clock(event.Timestamp)+" ", "text-muted").Append(fmt.Sprintf("⚠ %d events dropped: the agent's buffer was full", event.Dropped), "text-yellow-600")}
	case "enter":
		opened := &call{event: event}
		if parent := c.open[frameKey{event.ThreadID, event.Parent}]; event.Parent != 0 && parent != nil {
			opened.nested = true
			parent.children = append(parent.children, opened)
		}
		c.open[key] = opened
	case "return":
		if opened := c.open[key]; opened != nil {
			opened.value = event.Value
		}
	case "throw":
		if opened := c.open[key]; opened != nil {
			opened.err = event.Error
			c.stats.Threw++
		}
	case "exit":
		opened := c.open[key]
		if opened == nil {
			return nil
		}
		delete(c.open, key)
		opened.duration = time.Duration(event.DurationNs)
		if opened.nested {
			return nil
		}
		c.stats.Calls++
		if opened.duration >= c.stats.Longest {
			c.stats.Longest, c.stats.Slowest = opened.duration, opened.name()
		}
		return c.lines(opened, threadName(opened.event), "", "")
	}
	return nil
}

// EventOf reads a stored jvm_trace row back into its event; a null column is unset.
func EventOf(row map[string]any) (Event, error) {
	set := map[string]any{}
	for name, value := range row {
		if value != nil {
			set[name] = value
		}
	}
	data, err := json.Marshal(set)
	if err != nil {
		return Event{}, fmt.Errorf("encode jvm_trace row: %w", err)
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, fmt.Errorf("decode jvm_trace row: %w", err)
	}
	return event, nil
}

// Stats are the calls folded so far.
func (c *Calls) Stats() Stats { return c.stats }

// lines are a call's line and, below it, its nested calls' as a tree. A
// nested call's enter carries no thread name, so every line shows its root's.
func (c *Calls) lines(done *call, thread, branch, indent string) []api.Text {
	line := api.Text{}.Append(c.clock(done.event.Timestamp)+" "+thread+" ", "text-muted").Append(branch, "text-muted").Append(done.name(), "font-bold")
	if !done.nested {
		line = line.Append("(" + strings.Join(values(done.event.Args), ", ") + ")")
	}
	if done.err != "" {
		line = line.Append(" ✗ "+shortError(done.err), "text-red-600")
	} else if len(done.value) > 0 {
		line = line.Append(" → " + value(done.value))
	}
	result := []api.Text{line.Append(" "+formatDuration(done.duration), "text-muted")}
	for i, child := range done.children {
		mark, next := "├─ ", "│  "
		if i == len(done.children)-1 {
			mark, next = "└─ ", "   "
		}
		result = append(result, c.lines(child, thread, indent+mark, indent+next)...)
	}
	return result
}

func threadName(event Event) string {
	if event.Thread != "" {
		return event.Thread
	}
	return fmt.Sprintf("thread-%d", event.ThreadID)
}

func (c *Calls) clock(at time.Time) string {
	location := c.Location
	if location == nil {
		location = time.Local
	}
	return at.In(location).Format("15:04:05.000")
}

func (done *call) name() string { return shortType(done.event.Class) + "." + done.event.Method }

// Pretty is the stats on one line: calls, failures, the slowest and any loss.
func (s Stats) Pretty() api.Text {
	parts := []string{}
	switch s.Calls {
	case 0:
		parts = append(parts, "no calls completed")
	case 1:
		parts = append(parts, "1 call")
	default:
		parts = append(parts, fmt.Sprintf("%d calls", s.Calls))
	}
	if s.Threw > 0 {
		parts = append(parts, fmt.Sprintf("%d threw", s.Threw))
	}
	if s.Slowest != "" {
		parts = append(parts, "slowest "+s.Slowest+" "+formatDuration(s.Longest))
	}
	if s.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d events dropped", s.Dropped))
	}
	return api.Text{}.Append(strings.Join(parts, " · "))
}

// node is one value of a call as the agent's object graph renders it.
type node struct {
	Kind     string `json:"kind"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Children []any  `json:"children"`
}

func values(raw json.RawMessage) []string {
	var nodes []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &nodes) != nil {
		return nil
	}
	result := make([]string, len(nodes))
	for i, data := range nodes {
		result[i] = value(data)
	}
	return result
}

// value is a scalar as itself, a string quoted, a null as null, and anything
// else by its short type with its size or member count.
func value(raw json.RawMessage) string {
	var v node
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch {
	case v.Kind == "scalar" && (v.Type == "null" || v.Type == ""):
		if v.Value == "" {
			return "null"
		}
		return v.Value
	case v.Kind == "scalar" && v.Type == "java.lang.String":
		return fmt.Sprintf("%q", v.Value)
	case v.Kind == "scalar":
		return v.Value
	case v.Value != "":
		return shortType(v.Type) + "{" + v.Value + "}"
	}
	return fmt.Sprintf("%s{%d}", shortType(v.Type), len(v.Children))
}

func shortType(name string) string { return name[strings.LastIndex(name, ".")+1:] }

// shortError is an exception with its class's simple name.
func shortError(text string) string {
	class, message, found := strings.Cut(text, ": ")
	if !found || strings.Contains(class, " ") {
		return text
	}
	return shortType(class) + ": " + message
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", (d+500*time.Nanosecond)/time.Microsecond)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
