package jvmruntime_test

import (
	"encoding/json"
	"strings"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const liveThreads = `{"sampledMs":100,"cpuTimeEnabled":true,"deadlocked":[],"threads":[
{"id":3,"name":"main","state":"TIMED_WAITING","daemon":false,"priority":5,"cpuPercent":0.1,"cpuTimeMs":443,"blockedCount":0,"blockedTimeMs":null,"waitedCount":1282,"waitedTimeMs":null,"lockName":null,"lockOwnerId":null,"lockOwnerName":null,"inNative":false,"suspended":false},
{"id":15,"name":"Finalizer","state":"WAITING","daemon":true,"priority":8,"cpuPercent":0.0,"cpuTimeMs":0,"blockedCount":0,"blockedTimeMs":null,"waitedCount":1,"waitedTimeMs":null,"lockName":"java.lang.ref.ReferenceQueue$Lock@7ce5d946","lockOwnerId":null,"lockOwnerName":null,"inNative":false,"suspended":false},
{"id":27,"name":"telltale-agent","state":"RUNNABLE","daemon":true,"priority":9,"cpuPercent":0.0,"cpuTimeMs":3,"blockedCount":0,"blockedTimeMs":null,"waitedCount":0,"waitedTimeMs":null,"lockName":null,"lockOwnerId":null,"lockOwnerName":null,"inNative":true,"suspended":false}]}`

const liveDump = "\"main\" #3 prio=5\n   java.lang.Thread.State: TIMED_WAITING\n\tat java.lang.Thread.sleepNanos0(Native Method)\n\tat Idle.main(Unknown Source)\n\n   Locked ownable synchronizers:\n\t- None\n\n" +
	"\"Finalizer\" #15 daemon prio=8\n   java.lang.Thread.State: WAITING\n\tat java.lang.Object.wait0(Native Method)\n\t- waiting on <0x7ce5d946> (a java.lang.ref.ReferenceQueue$Lock)\n\tat java.lang.ref.Finalizer$FinalizerThread.run(Finalizer.java:165)\n\n   Locked ownable synchronizers:\n\t- None\n\n" +
	"\"Signal Dispatcher\" #16 daemon prio=9\n   java.lang.Thread.State: RUNNABLE\n\n   Locked ownable synchronizers:\n\t- None\n\n" +
	"\"telltale-agent\" #27 daemon prio=9\n   java.lang.Thread.State: RUNNABLE\n\tat sun.nio.ch.Net.accept(Native Method)\n\n   Locked ownable synchronizers:\n\t- <0x3bd736a7> (a java.util.concurrent.locks.ReentrantLock$NonfairSync)\n\n"

const deadlockTrailer = "Found 1 deadlock:\n\n\"worker-a\" Id=31:\n  waiting for <0x1> (a java.lang.Object),\n  which is held by \"worker-b\" Id=32\n"

const liveStack = `{"thread":{"id":15,"name":"Finalizer","state":"WAITING","daemon":true,"priority":8,"cpuPercent":null,"cpuTimeMs":0,"blockedCount":0,"blockedTimeMs":null,"waitedCount":1,"waitedTimeMs":null,"lockName":"java.lang.ref.ReferenceQueue$Lock@7ce5d946","lockOwnerId":null,"lockOwnerName":null,"inNative":false,"suspended":false},
"text":"\"Finalizer\" #15 daemon prio=8\n   java.lang.Thread.State: WAITING\n\tat java.lang.Object.wait0(Native Method)\n\t- waiting on <0x7ce5d946> (a java.lang.ref.ReferenceQueue$Lock)\n\tat java.lang.ref.Finalizer$FinalizerThread.run(Finalizer.java:165)\n\n   Locked ownable synchronizers:\n\t- None\n",
"frames":[{"className":"java.lang.Object","methodName":"wait0","fileName":"Object.java","lineNumber":-2},{"className":"java.lang.ref.Finalizer$FinalizerThread","methodName":"run","fileName":"Finalizer.java","lineNumber":165}]}`

func jsonString(text string) json.RawMessage {
	raw, err := json.Marshal(text)
	Expect(err).NotTo(HaveOccurred())
	return raw
}

var _ = Describe("thread sample", func() {
	It("decodes every thread row the agent sends", func() {
		sample, err := jvmruntime.DecodeThreads(json.RawMessage(liveThreads))
		Expect(err).NotTo(HaveOccurred())
		Expect(sample.Threads[1]).To(Equal(jvmruntime.Thread{
			ID: 15, Name: "Finalizer", State: "WAITING", Daemon: true, Priority: 8, CPUPercent: new(0.0), CPUTimeMs: new(int64(0)),
			WaitedCount: 1, LockName: new("java.lang.ref.ReferenceQueue$Lock@7ce5d946"),
		}))
		Expect(sample.SampledMs).To(Equal(100))
		Expect(sample.Deadlocked).To(Equal([]int64{}))
	})

	It("heads the table with the sample window", func() {
		sample, err := jvmruntime.DecodeThreads(json.RawMessage(liveThreads))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(sample.Pretty())[0]).To(Equal("Threads 3 threads · sampled 100ms"))
	})

	It("flags deadlocked threads and disabled CPU timing in the heading", func() {
		sample, err := jvmruntime.DecodeThreads(json.RawMessage(strings.Replace(strings.Replace(liveThreads, `"deadlocked":[]`, `"deadlocked":[3,15]`, 1), `"cpuTimeEnabled":true`, `"cpuTimeEnabled":false`, 1)))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(sample.Pretty())[0]).To(Equal("Threads 3 threads · sampled 100ms · CPU time off · DEADLOCK threads 3, 15"))
	})

	It("tabulates each thread with a colored state", func() {
		row := jvmruntime.Thread{ID: 31, Name: "worker-a", State: "BLOCKED", CPUPercent: new(12.5)}
		Expect(row.Row()).To(Equal(map[string]any{
			"id": int64(31), "name": "worker-a", "state": api.Text{}.Append("BLOCKED", "text-red-600"), "cpu": "12.5%", "daemon": false,
		}))
		Expect(jvmruntime.Thread{State: "RUNNABLE"}.Row()["state"]).To(Equal(api.Text{}.Append("RUNNABLE", "text-green-600")))
		Expect(jvmruntime.Thread{State: "TIMED_WAITING"}.Row()["state"]).To(Equal(api.Text{}.Append("TIMED_WAITING", "text-muted")))
		Expect(jvmruntime.Thread{}.Row()["cpu"]).To(Equal(""))
	})

	It("refuses a result that is not a thread sample", func() {
		for _, raw := range []string{`{}`, `{"sampledMs":100,"cpuTimeEnabled":true,"deadlocked":[],"threads":[]}`, `{"sampledMs":100,"cpuTimeEnabled":true,"threads":[{"id":1}]}`, `[]`} {
			_, err := jvmruntime.DecodeThreads(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})

var _ = Describe("thread dump", func() {
	It("parses the agent's jstack text into threads", func() {
		dump, err := jvmruntime.DecodeThreadDump(jsonString(liveDump))
		Expect(err).NotTo(HaveOccurred())
		Expect(dump).To(Equal(jvmruntime.ThreadDump{Threads: []jvmruntime.DumpedThread{
			{Name: "main", ID: 3, Priority: 5, State: "TIMED_WAITING", Stack: []string{"at java.lang.Thread.sleepNanos0(Native Method)", "at Idle.main(Unknown Source)"}},
			{Name: "Finalizer", ID: 15, Daemon: true, Priority: 8, State: "WAITING", Stack: []string{
				"at java.lang.Object.wait0(Native Method)", "- waiting on <0x7ce5d946> (a java.lang.ref.ReferenceQueue$Lock)", "at java.lang.ref.Finalizer$FinalizerThread.run(Finalizer.java:165)",
			}},
			{Name: "Signal Dispatcher", ID: 16, Daemon: true, Priority: 9, State: "RUNNABLE", Stack: []string{}},
			{Name: "telltale-agent", ID: 27, Daemon: true, Priority: 9, State: "RUNNABLE", Stack: []string{"at sun.nio.ch.Net.accept(Native Method)"},
				Synchronizers: []string{"<0x3bd736a7> (a java.util.concurrent.locks.ReentrantLock$NonfairSync)"}},
		}}))
	})

	It("renders each thread as a header over its indented frames", func() {
		dump, err := jvmruntime.DecodeThreadDump(jsonString(liveDump))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(dump.Pretty())).To(Equal([]string{
			"Thread dump 4 threads",
			"main #3 · prio 5 · TIMED_WAITING",
			"  at java.lang.Thread.sleepNanos0(Native Method)",
			"  at Idle.main(Unknown Source)",
			"Finalizer #15 · daemon · prio 8 · WAITING",
			"  at java.lang.Object.wait0(Native Method)",
			"  - waiting on <0x7ce5d946> (a java.lang.ref.ReferenceQueue$Lock)",
			"  at java.lang.ref.Finalizer$FinalizerThread.run(Finalizer.java:165)",
			"Signal Dispatcher #16 · daemon · prio 9 · RUNNABLE",
			"telltale-agent #27 · daemon · prio 9 · RUNNABLE",
			"  at sun.nio.ch.Net.accept(Native Method)",
			"  locked synchronizers: <0x3bd736a7> (a java.util.concurrent.locks.ReentrantLock$NonfairSync)",
		}))
	})

	It("keeps the deadlock report and counts it in the heading", func() {
		dump, err := jvmruntime.DecodeThreadDump(jsonString(liveDump + deadlockTrailer))
		Expect(err).NotTo(HaveOccurred())
		Expect(dump.Deadlocks).To(Equal([]string{"Found 1 deadlock:", `"worker-a" Id=31:`, "  waiting for <0x1> (a java.lang.Object),", `  which is held by "worker-b" Id=32`}))
		rendered := lines(dump.Pretty())
		Expect(rendered[0]).To(Equal("Thread dump 4 threads · DEADLOCK"))
		Expect(rendered[len(rendered)-4:]).To(Equal(dump.Deadlocks))
	})

	It("refuses anything that is not jstack text", func() {
		for _, raw := range []json.RawMessage{json.RawMessage(`{}`), jsonString(""), jsonString("not a dump\n"), jsonString("\"x\" #1 prio=5\n\tunexpected\n"), jsonString("\"x\" #1 prio=5\n")} {
			_, err := jvmruntime.DecodeThreadDump(raw)
			Expect(err).To(HaveOccurred(), string(raw))
		}
	})
})

var _ = Describe("thread stack", func() {
	It("decodes the thread row, text and frames", func() {
		stack, err := jvmruntime.DecodeThreadStack(json.RawMessage(liveStack))
		Expect(err).NotTo(HaveOccurred())
		Expect(stack.Thread.ID).To(Equal(int64(15)))
		Expect(stack.Thread.CPUPercent).To(BeNil())
		Expect(stack.Frames).To(Equal([]jvmruntime.StackFrame{
			{ClassName: "java.lang.Object", MethodName: "wait0", FileName: new("Object.java"), LineNumber: -2},
			{ClassName: "java.lang.ref.Finalizer$FinalizerThread", MethodName: "run", FileName: new("Finalizer.java"), LineNumber: 165},
		}))
	})

	It("renders the thread header with its CPU time over its frames and locks", func() {
		stack, err := jvmruntime.DecodeThreadStack(json.RawMessage(liveStack))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines(stack.Pretty())).To(Equal([]string{
			"Finalizer #15 · daemon · prio 8 · WAITING · cpu 0ms",
			"  at java.lang.Object.wait0(Native Method)",
			"  - waiting on <0x7ce5d946> (a java.lang.ref.ReferenceQueue$Lock)",
			"  at java.lang.ref.Finalizer$FinalizerThread.run(Finalizer.java:165)",
		}))
	})

	It("refuses a stack whose text is not the requested thread", func() {
		other := strings.Replace(liveStack, `{"id":15,`, `{"id":16,`, 1)
		for _, raw := range []string{`{}`, `{"thread":{"id":15},"frames":[]}`, other} {
			_, err := jvmruntime.DecodeThreadStack(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})
