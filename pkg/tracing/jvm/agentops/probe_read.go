package agentops

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

// ProbesReadOp reads a probe's buffered events forward from a sequence number.
const ProbesReadOp = "jvm.probes.read"

// ProbeRead is one read of a probe's ring. A closed probe carries its final
// counters and no events. GC is the span's {count, pauseMs} when measured.
type ProbeRead struct {
	Key        string          `json:"key"`
	Generation int64           `json:"generation"`
	Events     []ProbeEvent    `json:"events"`
	NextSeq    int64           `json:"nextSeq,omitempty"`
	OldestSeq  int64           `json:"oldestSeq,omitempty"`
	WriteSeq   int64           `json:"writeSeq"`
	Dropped    int64           `json:"dropped,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
	Suppressed int64           `json:"suppressed,omitempty"`
	Filtered   int64           `json:"filtered"`
	Omitted    int64           `json:"omitted"`
	Degraded   bool            `json:"degraded,omitempty"`
	Capacity   int             `json:"capacity"`
	GC         json.RawMessage `json:"gc,omitempty"`
	Active     bool            `json:"active"`
	Closed     bool            `json:"closed"`
	Complete   bool            `json:"complete,omitempty"`
	MaxEvents  int             `json:"maxEvents,omitempty"`
	Owners     []string        `json:"owners,omitempty"`
}

// ProbeEvent is one event as the JVM rendered it. TS is epoch nanoseconds;
// exit, return and throw events name their method only through Frame.
type ProbeEvent struct {
	Seq        int64  `json:"seq"`
	TS         int64  `json:"ts"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	ThreadID   int64  `json:"threadId"`
	Thread     string `json:"thread,omitempty"`
	Depth      int    `json:"depth"`
	Frame      int64  `json:"frame"`
	Parent     int64  `json:"parent,omitempty"`
	Class      string `json:"class,omitempty"`
	Method     string `json:"method,omitempty"`
	Descriptor string `json:"descriptor,omitempty"`
	Line       int    `json:"line,omitempty"`
	Args       []Node `json:"args,omitempty"`
	Value      *Node  `json:"value,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationNs int64  `json:"durationNs,omitempty"`
	AllocBytes *int64 `json:"allocBytes,omitempty"`
	GCCount    *int64 `json:"gcCount,omitempty"`
	GCPauseMs  *int64 `json:"gcPauseMs,omitempty"`
	Unwound    int    `json:"unwound,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Omitted    int    `json:"omitted,omitempty"`
}

// DecodeProbeRead decodes a read, parsing each event line the JVM rendered,
// and fails when the agent reports the probe is not open.
func DecodeProbeRead(raw json.RawMessage) (ProbeRead, error) {
	var wire struct {
		ProbeRead
		Events []string `json:"events"`
		Error  string   `json:"error"`
	}
	if err := decode(ProbesReadOp, raw, &wire, "key"); err != nil {
		return ProbeRead{}, err
	}
	if wire.Error != "" {
		return ProbeRead{}, fmt.Errorf("%s: %s", ProbesReadOp, wire.Error)
	}
	if wire.Events == nil {
		return ProbeRead{}, fmt.Errorf("%s returned no events: %s", ProbesReadOp, raw)
	}
	read := wire.ProbeRead
	read.Events = make([]ProbeEvent, len(wire.Events))
	for i, line := range wire.Events {
		if err := json.Unmarshal([]byte(line), &read.Events[i]); err != nil {
			return ProbeRead{}, fmt.Errorf("decode %s event %d %q: %w", ProbesReadOp, i, line, err)
		}
	}
	return read, nil
}

// Pretty is a heading with the read's position and state over one line per event.
func (r ProbeRead) Pretty() api.Text {
	heading := api.Text{}.Append("Probe "+r.Key, "font-bold")
	if r.Closed {
		return heading.Append(" closed · "+plural(r.WriteSeq, "event", "events")+" written", "text-muted")
	}
	facts := []string{plural(int64(len(r.Events)), "event", "events")}
	if len(r.Events) > 0 {
		facts[0] += fmt.Sprintf(", seq %d–%d of %d", r.Events[0].Seq, r.Events[len(r.Events)-1].Seq, r.WriteSeq)
	}
	facts = append(facts, r.state())
	if r.Truncated {
		facts = append(facts, fmt.Sprintf("more from --since %d", r.NextSeq))
	}
	text := heading.Append(" "+strings.Join(facts, " · "), "text-muted")
	if r.Dropped > 0 {
		text = text.Append(fmt.Sprintf(" · ⚠ %d evicted before this read", r.Dropped), "text-yellow-600")
	}
	names := map[frameKey]string{}
	for _, event := range r.Events {
		text = text.NewLine().Add(event.line(names))
	}
	return text
}

func (r ProbeRead) state() string {
	switch {
	case r.Complete:
		return "complete"
	case r.Active:
		return "recording"
	}
	return "frozen"
}

type frameKey struct{ thread, frame int64 }

// line is the event as "time thread kind Class.method…", naming the method of
// an exit, return or throw from the enter of the same frame in names.
func (e ProbeEvent) line(names map[frameKey]string) api.Text {
	key := frameKey{e.ThreadID, e.Frame}
	name := names[key]
	if e.Method != "" {
		name = shortType(e.Class) + "." + e.Method
		names[key] = name
	}
	if name == "" {
		name = fmt.Sprintf("frame %d", e.Frame)
	}
	thread := e.Thread
	if thread == "" {
		thread = fmt.Sprintf("thread-%d", e.ThreadID)
	}
	line := api.Text{}.Append(clock(time.Unix(0, e.TS))+" "+thread+" "+e.Kind+" ", "text-muted").Append(name, "font-bold")
	switch e.Kind {
	case "enter":
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = arg.compact()
		}
		line = line.Append("(" + strings.Join(args, ", ") + ")")
	case "return":
		if e.Value != nil {
			line = line.Append(" → " + e.Value.compact())
		}
	case "throw", "error":
		line = line.Append(" ✗ "+shortError(e.Error), "text-red-600")
	case "exit":
		line = line.Append(" "+formatDuration(time.Duration(e.DurationNs)), "text-muted")
	}
	return line
}
