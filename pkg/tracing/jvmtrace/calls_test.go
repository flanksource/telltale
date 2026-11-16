package jvmtrace_test

import (
	"encoding/json"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("folding method events into calls", func() {
	start := time.Date(2026, 11, 16, 0, 5, 9, 912_000_000, time.UTC)
	at := func(offset time.Duration) time.Time { return start.Add(offset) }
	scalar := func(kind, value string) json.RawMessage {
		data, err := json.Marshal(map[string]string{"kind": "scalar", "type": kind, "value": value})
		Expect(err).NotTo(HaveOccurred())
		return data
	}
	lines := func(calls *jvmtrace.Calls, events ...jvmtrace.Event) []string {
		var printed []string
		for _, event := range events {
			for _, line := range calls.Add(event) {
				printed = append(printed, line.String())
			}
		}
		return printed
	}

	It("prints a watched call as one line once it exits: time, thread, call with its arguments, value and duration", func() {
		calls := &jvmtrace.Calls{Location: time.UTC}
		Expect(lines(calls,
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), Thread: "main", ThreadID: 3, Frame: 1, Class: "example.Idle", Method: "work", Args: json.RawMessage(`[` + string(scalar("java.lang.Long", "100045")) + `,` + string(scalar("java.lang.String", "fast")) + `]`)},
			jvmtrace.Event{Kind: "return", Timestamp: at(time.Millisecond), ThreadID: 3, Frame: 1, Value: scalar("java.lang.Long", "450198")},
			jvmtrace.Event{Kind: "exit", Timestamp: at(2 * time.Millisecond), ThreadID: 3, Frame: 1, DurationNs: 1_996_875},
		)).To(Equal([]string{`00:05:09.912 main Idle.work(100045, "fast") → 450198 2.0ms`}))
		Expect(calls.Stats().Pretty().String()).To(Equal("1 call · slowest Idle.work 2.0ms"))
	})

	It("prints a traced call with the calls nested inside it as a tree once the outermost call exits", func() {
		calls := &jvmtrace.Calls{Location: time.UTC}
		printed := lines(calls,
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), Thread: "main", ThreadID: 3, Frame: 1, Class: "Idle", Method: "work", Args: json.RawMessage(`[` + string(scalar("java.lang.Long", "100045")) + `]`)},
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), ThreadID: 3, Depth: 1, Frame: 2, Parent: 1, Class: "Idle", Method: "step"},
			jvmtrace.Event{Kind: "exit", ThreadID: 3, Depth: 1, Frame: 2, DurationNs: 1_457_917},
			jvmtrace.Event{Kind: "enter", Timestamp: at(2 * time.Millisecond), ThreadID: 3, Depth: 1, Frame: 3, Parent: 1, Class: "Idle", Method: "step"},
			jvmtrace.Event{Kind: "enter", Timestamp: at(2 * time.Millisecond), ThreadID: 3, Depth: 2, Frame: 4, Parent: 3, Class: "Idle", Method: "check"},
			jvmtrace.Event{Kind: "throw", ThreadID: 3, Depth: 2, Frame: 4, Error: "java.lang.IllegalStateException: negative -1"},
			jvmtrace.Event{Kind: "exit", ThreadID: 3, Depth: 2, Frame: 4, DurationNs: 41_000},
			jvmtrace.Event{Kind: "exit", ThreadID: 3, Depth: 1, Frame: 3, DurationNs: 173_708},
		)
		Expect(printed).To(BeEmpty(), "nothing prints until the outermost call exits")
		Expect(lines(calls,
			jvmtrace.Event{Kind: "return", ThreadID: 3, Frame: 1, Value: scalar("java.lang.Long", "450198")},
			jvmtrace.Event{Kind: "exit", ThreadID: 3, Frame: 1, DurationNs: 1_996_875},
		)).To(Equal([]string{
			"00:05:09.912 main Idle.work(100045) → 450198 2.0ms",
			"00:05:09.912 main ├─ Idle.step 1.5ms",
			"00:05:09.914 main └─ Idle.step 174µs",
			"00:05:09.914 main    └─ Idle.check ✗ IllegalStateException: negative -1 41µs",
		}))
		Expect(calls.Stats().Pretty().String()).To(Equal("1 call · 1 threw · slowest Idle.work 2.0ms"))
	})

	It("shows an object value by its short type and size, and a null as null", func() {
		calls := &jvmtrace.Calls{Location: time.UTC}
		Expect(lines(calls,
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), Thread: "http-1", ThreadID: 9, Frame: 7, Class: "com.example.Orders", Method: "find",
				Args: json.RawMessage(`[{"kind":"scalar","type":"null"},{"kind":"map","type":"java.util.HashMap","value":"size=3"}]`)},
			jvmtrace.Event{Kind: "return", ThreadID: 9, Frame: 7, Value: json.RawMessage(`{"kind":"object","type":"com.example.Order","children":[{"kind":"scalar"},{"kind":"scalar"}]}`)},
			jvmtrace.Event{Kind: "exit", ThreadID: 9, Frame: 7, DurationNs: 3_200_000_000},
		)).To(Equal([]string{"00:05:09.912 http-1 Orders.find(null, HashMap{size=3}) → Order{2} 3.20s"}))
	})

	It("reports events the agent dropped on their own line and in the stats", func() {
		calls := &jvmtrace.Calls{Location: time.UTC}
		Expect(lines(calls, jvmtrace.Event{Kind: "dropped", Timestamp: at(0), Dropped: 12})).To(Equal([]string{"00:05:09.912 ⚠ 12 events dropped: the agent's buffer was full"}))
		Expect(calls.Stats().Pretty().String()).To(Equal("no calls completed · 12 events dropped"))
	})

	It("reads a stored row back into its event, with unset columns left empty", func() {
		event, err := jvmtrace.EventOf(map[string]any{
			"kind": "return", "threadId": int64(3), "frame": int64(1), "timestamp": start, "class": nil, "args": nil, "durationNs": nil,
			"value": map[string]any{"kind": "scalar", "type": "java.lang.Long", "value": "450198"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(event).To(Equal(jvmtrace.Event{Kind: "return", ThreadID: 3, Frame: 1, Timestamp: start, Value: json.RawMessage(`{"kind":"scalar","type":"java.lang.Long","value":"450198"}`)}))
		_, err = jvmtrace.EventOf(map[string]any{"kind": "enter", "frame": "one"})
		Expect(err).To(MatchError(ContainSubstring("frame")))
	})

	It("keeps calls on different threads apart", func() {
		calls := &jvmtrace.Calls{Location: time.UTC}
		Expect(lines(calls,
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), Thread: "a", ThreadID: 1, Frame: 1, Class: "W", Method: "run"},
			jvmtrace.Event{Kind: "enter", Timestamp: at(0), Thread: "b", ThreadID: 2, Frame: 1, Class: "W", Method: "run"},
			jvmtrace.Event{Kind: "exit", ThreadID: 2, Frame: 1, DurationNs: 5_000},
			jvmtrace.Event{Kind: "exit", ThreadID: 1, Frame: 1, DurationNs: 9_000},
		)).To(Equal([]string{"00:05:09.912 b W.run() 5µs", "00:05:09.912 a W.run() 9µs"}))
	})
})
