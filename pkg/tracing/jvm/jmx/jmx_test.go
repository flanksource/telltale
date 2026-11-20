package jmx_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jmx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJMX(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "JMX, notification and logging results")
}

const (
	agentSearch = `["java.lang:type=Runtime","java.lang:name=G1 Old Gen,type=MemoryPool","java.lang:type=Memory"]`

	agentMemoryInfo = `{"objectName":"java.lang:type=Memory","className":"sun.management.MemoryImpl","description":"Information on the management interface of the MBean",
"attributes":[{"name":"HeapMemoryUsage","description":"HeapMemoryUsage","type":"javax.management.openmbean.CompositeData","readable":true,"writable":false,"is":false},{"name":"Verbose","description":"Verbose","type":"boolean","readable":true,"writable":true,"is":true}],
"operations":[{"name":"gc","description":"gc","returnType":"void","impact":3,"signature":[]}],
"notifications":[{"name":"javax.management.Notification","description":"Memory Notification","types":["java.management.memory.threshold.exceeded","java.management.memory.collection.threshold.exceeded"]}]}`

	agentThreadingInfo = `{"objectName":"java.lang:type=Threading","className":"com.sun.management.internal.HotSpotThreadImpl","description":"Information on the management interface of the MBean",
"attributes":[{"name":"ThreadCount","description":"ThreadCount","type":"int","readable":true,"writable":false,"is":false},{"name":"ThreadCpuTimeEnabled","description":"ThreadCpuTimeEnabled","type":"boolean","readable":true,"writable":true,"is":true},{"name":"AllThreadIds","description":"AllThreadIds","type":"[J","readable":true,"writable":false,"is":false}],
"operations":[{"name":"getThreadCpuTime","description":"getThreadCpuTime","returnType":"[J","impact":3,"signature":[{"name":"p0","description":"p0","type":"[J"}]},{"name":"getThreadInfo","description":"getThreadInfo","returnType":"[Ljavax.management.openmbean.CompositeData;","impact":3,"signature":[{"name":"p0","description":"p0","type":"[J"},{"name":"p1","description":"p1","type":"int"}]},{"name":"resetPeakThreadCount","description":"resetPeakThreadCount","returnType":"void","impact":3,"signature":[]}],
"notifications":[]}`

	agentReadAll = `{"objectName":"java.lang:type=Memory","attributes":[{"name":"ObjectPendingFinalizationCount","value":0},{"name":"HeapMemoryUsage","value":{"committed":50331648,"init":536870912,"max":8589934592,"used":13813496}},{"name":"NonHeapMemoryUsage","value":{"committed":40173568,"init":7667712,"max":-1,"used":37357104}},{"name":"Verbose","value":false},{"name":"ObjectName","value":"java.lang:type=Memory"}]}`

	agentReadOne = `{"committed":50331648,"init":536870912,"max":8589934592,"used":13813496}`

	agentSubscriptions = `[{"id":"6f1c2a9e-4b7d-4c1e-9a53-0d2b8e7f1a40","mbean":"java.lang:type=GarbageCollector,*","filters":["com.sun.management.gc.notification"],"capacity":500,"queued":3,"dropped":0,"active":true},
{"id":"0b9e5d12-77aa-4f0c-8c11-5e6f7a8b9c0d","mbean":"java.lang:type=Memory","filters":null,"capacity":50,"queued":0,"dropped":12,"active":false}]`

	agentLoggers = `[{"name":"","level":"INFO","parent":null},{"name":"global","level":null,"parent":""},{"name":"org.benf.cfr.reader.entities.constantpool.ConstantPool","level":"WARNING","parent":""}]`
)

func decode(op, raw string) any {
	result, err := jmx.Results[op](json.RawMessage(raw))
	Expect(err).NotTo(HaveOccurred(), op)
	return result
}

func pretty(result any) string {
	return result.(interface{ Pretty() api.Text }).Pretty().String()
}

func ptr(s string) *string { return &s }

var _ = Describe("jmx search", func() {
	It("lists every matched MBean in name order under its count", func() {
		Expect(pretty(decode(jmx.SearchOp, agentSearch))).To(Equal(strings.Join([]string{
			"MBeans 3 matched",
			"java.lang:name=G1 Old Gen,type=MemoryPool",
			"java.lang:type=Memory",
			"java.lang:type=Runtime",
		}, "\n")))
	})

	It("says so in one line when nothing matched", func() {
		Expect(pretty(decode(jmx.SearchOp, `[]`))).To(Equal("no MBeans matched"))
	})
})

var _ = Describe("jmx info", func() {
	It("decodes the MBean's attributes, operations and notifications", func() {
		Expect(decode(jmx.InfoOp, agentMemoryInfo)).To(Equal(jmx.MBeanInfo{
			ObjectName: "java.lang:type=Memory", ClassName: "sun.management.MemoryImpl", Description: "Information on the management interface of the MBean",
			Attributes: []jmx.MBeanAttribute{
				{Name: "HeapMemoryUsage", Description: "HeapMemoryUsage", Type: "javax.management.openmbean.CompositeData", Readable: true},
				{Name: "Verbose", Description: "Verbose", Type: "boolean", Readable: true, Writable: true, Is: true},
			},
			Operations: []jmx.MBeanOperation{{Name: "gc", Description: "gc", ReturnType: "void", Impact: 3, Signature: []jmx.MBeanParameter{}}},
			Notifications: []jmx.MBeanNotification{{Name: "javax.management.Notification", Description: "Memory Notification",
				Types: []string{"java.management.memory.threshold.exceeded", "java.management.memory.collection.threshold.exceeded"}}},
		}))
	})

	It("renders operations as signatures with readable Java types", func() {
		Expect(pretty(decode(jmx.InfoOp, agentThreadingInfo))).To(Equal(strings.Join([]string{
			"java.lang:type=Threading com.sun.management.internal.HotSpotThreadImpl · Information on the management interface of the MBean",
			"╭────────────────────┬───────┬──────────╮",
			"│Attribute           │Type   │Access    │",
			"├────────────────────┼───────┼──────────┤",
			"│ThreadCount         │int    │read-only │",
			"│ThreadCpuTimeEnabled│boolean│read-write│",
			"│AllThreadIds        │long[] │read-only │",
			"╰────────────────────┴───────┴──────────╯",
			"Operations",
			"getThreadCpuTime(long[]) → long[]",
			"getThreadInfo(long[], int) → CompositeData[]",
			"resetPeakThreadCount() → void",
		}, "\n")))
	})

	It("tabulates attributes with their simple type and access", func() {
		row := jmx.MBeanAttribute{Name: "AllThreadIds", Description: "AllThreadIds", Type: "[J", Readable: true}
		Expect(row.Row()).To(Equal(map[string]any{"name": "AllThreadIds", "type": "long[]", "access": "read-only", "description": ""}))
	})
})

var _ = Describe("jmx read", func() {
	It("prints every attribute as one compact line", func() {
		Expect(pretty(decode(jmx.ReadOp, agentReadAll))).To(Equal(strings.Join([]string{
			"java.lang:type=Memory 5 attributes",
			"ObjectPendingFinalizationCount = 0",
			`HeapMemoryUsage = {"committed":50331648,"init":536870912,"max":8589934592,"used":13813496}`,
			`NonHeapMemoryUsage = {"committed":40173568,"init":7667712,"max":-1,"used":37357104}`,
			"Verbose = false",
			"ObjectName = java.lang:type=Memory",
		}, "\n")))
	})

	It("prints a single attribute's value as one line and keeps it verbatim as JSON", func() {
		result := decode(jmx.ReadOp, agentReadOne)
		Expect(pretty(result)).To(Equal(agentReadOne))
		Expect(json.Marshal(result)).To(MatchJSON(agentReadOne))
	})
})

var _ = Describe("mutation acknowledgements", func() {
	It("acknowledges a write, an invocation and a level change in one line", func() {
		Expect(pretty(decode(jmx.WriteOp, `{"applied":true}`))).To(Equal("✓ attribute written"))
		Expect(pretty(decode(jmx.LoggingSetOp, `{"applied":true}`))).To(Equal("✓ logger level set"))
		Expect(pretty(decode(jmx.InvokeOp, `null`))).To(Equal("✓ invoked"))
		Expect(pretty(decode(jmx.InvokeOp, `-1`))).To(Equal("✓ invoked → -1"))
	})

	It("names what the request changed, since the agent echoes only that it applied it", func() {
		requested := func(op, params, raw string) string {
			result, err := decode(op, raw).(interface {
				Requested(json.RawMessage) (any, error)
			}).Requested(json.RawMessage(params))
			Expect(err).NotTo(HaveOccurred())
			return pretty(result)
		}
		Expect(requested(jmx.WriteOp, `{"objectName":"java.lang:type=Memory","attribute":"Verbose","value":false}`, `{"applied":true}`)).To(Equal("✓ java.lang:type=Memory Verbose = false"))
		Expect(requested(jmx.LoggingSetOp, `{"name":"global","level":"FINE"}`, `{"applied":true}`)).To(Equal("✓ logger global → FINE"))
		Expect(requested(jmx.LoggingSetOp, `{"name":"","level":null}`, `{"applied":true}`)).To(Equal("✓ logger (root) → inherit"))
		Expect(requested(jmx.InvokeOp, `{"objectName":"java.lang:type=Memory","operation":"gc"}`, `null`)).To(Equal("✓ invoked java.lang:type=Memory gc"))
		Expect(requested(jmx.InvokeOp, `{"objectName":"java.lang:type=Threading","operation":"getThreadCpuTime","args":[[1]]}`, `-1`)).To(Equal("✓ invoked java.lang:type=Threading getThreadCpuTime → -1"))
		_, err := decode(jmx.WriteOp, `{"applied":true}`).(interface {
			Requested(json.RawMessage) (any, error)
		}).Requested(json.RawMessage(`{"attribute":"Verbose"}`))
		Expect(err).To(MatchError(ContainSubstring("objectName")))
	})
})

var _ = Describe("notification subscriptions", func() {
	It("decodes every subscriber the agent buffers for", func() {
		Expect(decode(jmx.NotificationsListOp, agentSubscriptions)).To(Equal(jmx.Subscriptions{
			{ID: "6f1c2a9e-4b7d-4c1e-9a53-0d2b8e7f1a40", MBean: "java.lang:type=GarbageCollector,*", Filters: []string{"com.sun.management.gc.notification"}, Capacity: 500, Queued: 3, Active: true},
			{ID: "0b9e5d12-77aa-4f0c-8c11-5e6f7a8b9c0d", MBean: "java.lang:type=Memory", Capacity: 50, Dropped: 12},
		}))
	})

	It("heads the table with queued and dropped totals", func() {
		Expect(strings.SplitN(pretty(decode(jmx.NotificationsListOp, agentSubscriptions)), "\n", 2)[0]).
			To(Equal("Notification subscriptions 2 · 3 queued · 12 dropped"))
		Expect(pretty(decode(jmx.NotificationsListOp, `[]`))).To(Equal("no notification subscriptions"))
	})
})

var _ = Describe("logging list", func() {
	It("decodes loggers keeping the root's absent parent distinct from the root as parent", func() {
		Expect(decode(jmx.LoggingListOp, agentLoggers)).To(Equal(jmx.Loggers{
			{Name: "", Level: ptr("INFO")},
			{Name: "global", Parent: ptr("")},
			{Name: "org.benf.cfr.reader.entities.constantpool.ConstantPool", Level: ptr("WARNING"), Parent: ptr("")},
		}))
	})

	It("resolves each logger's effective level through its parents", func() {
		loggers := decode(jmx.LoggingListOp, agentLoggers).(jmx.Loggers)
		Expect(loggers.Rows()).To(Equal([]jmx.LoggerRow{
			{Logger: "(root)", Level: "INFO", Effective: "INFO"},
			{Logger: "global", Effective: "INFO"},
			{Logger: "org.benf.cfr.reader.entities.constantpool.ConstantPool", Level: "WARNING", Effective: "WARNING"},
		}))
		Expect(strings.SplitN(pretty(loggers), "\n", 2)[0]).To(Equal("Loggers 3 · 2 with explicit levels"))
	})
})

var _ = Describe("strict decoding", func() {
	DescribeTable("refuses what is not the operation's result",
		func(op, raw string) {
			_, err := jmx.Results[op](json.RawMessage(raw))
			Expect(err).To(HaveOccurred())
		},
		Entry("search object", jmx.SearchOp, `{}`),
		Entry("search null", jmx.SearchOp, `null`),
		Entry("info without class", jmx.InfoOp, `{"objectName":"java.lang:type=Memory"}`),
		Entry("info array", jmx.InfoOp, `[]`),
		Entry("read invalid json", jmx.ReadOp, `{`),
		Entry("read attributes without object name", jmx.ReadOp, `{"objectName":"","attributes":[]}`),
		Entry("write not applied", jmx.WriteOp, `{"applied":false}`),
		Entry("write empty", jmx.WriteOp, `{}`),
		Entry("logging set null", jmx.LoggingSetOp, `null`),
		Entry("invoke empty", jmx.InvokeOp, ``),
		Entry("subscriptions null", jmx.NotificationsListOp, `null`),
		Entry("subscription without id", jmx.NotificationsListOp, `[{"mbean":"java.lang:type=Memory"}]`),
		Entry("logger without name", jmx.LoggingListOp, `[{"level":"INFO","parent":null}]`),
		Entry("loggers without root", jmx.LoggingListOp, `[{"name":"global","level":null,"parent":""}]`),
		Entry("loggers object", jmx.LoggingListOp, `{}`),
	)
})
