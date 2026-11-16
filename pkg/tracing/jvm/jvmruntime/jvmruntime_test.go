package jvmruntime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJVMRuntime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "JVM runtime operation results")
}

func lines(text interface{ String() string }) []string {
	return strings.Split(text.String(), "\n")
}

var _ = Describe("registry", func() {
	It("decodes every runtime operation", func() {
		Expect(jvmruntime.Results).To(HaveLen(5))
		for _, op := range []string{jvmruntime.StatusOp, jvmruntime.MetricsOp, jvmruntime.ThreadsOp, jvmruntime.ThreadDumpOp, jvmruntime.ThreadStackOp} {
			Expect(jvmruntime.Results).To(HaveKey(op))
			_, err := jvmruntime.Results[op](json.RawMessage(`{}`))
			Expect(err).To(HaveOccurred(), op)
		}
	})

	It("decodes and renders each full capture from a live agent", func() {
		for op := range jvmruntime.Results {
			raw, err := os.ReadFile(filepath.Join("testdata", op+".json"))
			Expect(err).NotTo(HaveOccurred())
			result, err := jvmruntime.Results[op](raw)
			Expect(err).NotTo(HaveOccurred(), op)
			Expect(result.(interface{ Pretty() api.Text }).Pretty().String()).NotTo(BeEmpty(), op)
		}
	})
})

var _ = Describe("status", func() {
	const live = `{"allowMutation":true,"instrumentation":true,"patches":[]}`

	It("decodes the agent's status and renders it as one line", func() {
		status, err := jvmruntime.DecodeStatus(json.RawMessage(live))
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(jvmruntime.Status{AllowMutation: true, Instrumentation: true, Patches: []jvmruntime.Patch{}}))
		Expect(status.Pretty().String()).To(Equal("mutations allowed · instrumentation on · 0 patches"))
	})

	It("lists active patches compactly with their lease", func() {
		lease := time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC).UnixMilli()
		status, err := jvmruntime.DecodeStatus(json.RawMessage(`{"allowMutation":false,"instrumentation":true,"patches":[
{"class":"example.Cache","loader":"app","digest":"b","originalDigest":"a","patched":true,"leaseExpiresAt":` + strconv.FormatInt(lease, 10) + `},
{"class":"example.Pinned","loader":"app","digest":"b","originalDigest":"a","patched":true,"leaseExpiresAt":0},
{"class":"example.Gone","loader":"old","patched":false,"error":"no longer loaded: x"}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Patches[0]).To(Equal(jvmruntime.Patch{Class: "example.Cache", Loader: "app", Digest: "b", OriginalDigest: "a", Patched: true, LeaseExpiresAt: lease}))
		Expect(status.Pretty().String()).To(Equal("mutations denied · instrumentation on · 3 patches: example.Cache lease until 2026-11-16T05:00:00Z, example.Pinned no lease, example.Gone no longer loaded: x"))
	})

	It("says instrumentation is off without inventing a patch count", func() {
		status, err := jvmruntime.DecodeStatus(json.RawMessage(`{"allowMutation":true,"instrumentation":false}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Pretty().String()).To(Equal("mutations allowed · instrumentation off"))
	})

	It("refuses a result that is not a status", func() {
		for _, raw := range []string{`{}`, `{"allowMutation":true}`, `{"allowMutation":true,"instrumentation":true}`, `[]`, `"text"`} {
			_, err := jvmruntime.DecodeStatus(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})

var _ = Describe("metrics", func() {
	const live = `{"capturedAt":"2026-11-16T04:53:48.543631Z","memory":{"heap":[{"name":"G1 Eden Space","type":"HEAP","used":20971520,"committed":29360128,"max":-1},{"name":"G1 Old Gen","type":"HEAP","used":1230584,"committed":4194304,"max":8589934592}],"nonHeap":[{"name":"Metaspace","type":"NON_HEAP","used":24222544,"committed":24444928,"max":-1}],"buffer":[{"name":"direct","type":"BUFFER","used":0,"committed":0,"max":-1}]},"garbageCollectors":[{"name":"G1 Young Generation","collectionCount":2,"collectionTime":11},{"name":"G1 Old Generation","collectionCount":0,"collectionTime":0}],"runtime":{"javaVersion":"25.0.2","javaHome":"/opt/graalvm/Contents/Home","osName":"Mac OS X","osVersion":"26.5.2","processors":12,"systemLoadAverage":7.25341796875,"uptime":260192},"processCpuPercent":0.0,"connectors":[{"name":"\"http-nio-8080\"","objectName":"Catalina:name=\"http-nio-8080\",type=ThreadPool","currentThreadsBusy":3,"maxThreads":200}],"dataSources":[],"series":{"collectors":["G1 Young Generation","G1 Old Generation"],"connectors":["\"http-nio-8080\""],"dataSources":[]}}`

	It("decodes every metric the agent sends", func() {
		metrics, err := jvmruntime.DecodeMetrics(json.RawMessage(live))
		Expect(err).NotTo(HaveOccurred())
		Expect(metrics).To(Equal(jvmruntime.Metrics{
			CapturedAt: time.Date(2026, 11, 16, 4, 53, 48, 543631000, time.UTC),
			Memory: jvmruntime.Memory{
				Heap: []jvmruntime.MemoryPool{
					{Name: "G1 Eden Space", Type: "HEAP", Used: 20971520, Committed: 29360128, Max: -1},
					{Name: "G1 Old Gen", Type: "HEAP", Used: 1230584, Committed: 4194304, Max: 8589934592},
				},
				NonHeap: []jvmruntime.MemoryPool{{Name: "Metaspace", Type: "NON_HEAP", Used: 24222544, Committed: 24444928, Max: -1}},
				Buffer:  []jvmruntime.MemoryPool{{Name: "direct", Type: "BUFFER", Max: -1}},
			},
			GarbageCollectors: []jvmruntime.GarbageCollector{{Name: "G1 Young Generation", CollectionCount: 2, CollectionTime: 11}, {Name: "G1 Old Generation"}},
			Runtime:           jvmruntime.Runtime{JavaVersion: "25.0.2", JavaHome: "/opt/graalvm/Contents/Home", OSName: "Mac OS X", OSVersion: "26.5.2", Processors: 12, SystemLoadAverage: 7.25341796875, Uptime: 260192},
			ProcessCPUPercent: new(0.0),
			Connectors:        []map[string]any{{"name": `"http-nio-8080"`, "objectName": `Catalina:name="http-nio-8080",type=ThreadPool`, "currentThreadsBusy": 3.0, "maxThreads": 200.0}},
			DataSources:       []map[string]any{},
			Series:            jvmruntime.MetricSeries{Collectors: []string{"G1 Young Generation", "G1 Old Generation"}, Connectors: []string{`"http-nio-8080"`}, DataSources: []string{}},
		}))
	})

	It("renders a runtime heading, a memory pool table and one line per collector group", func() {
		metrics, err := jvmruntime.DecodeMetrics(json.RawMessage(live))
		Expect(err).NotTo(HaveOccurred())
		rendered := lines(metrics.Pretty())
		Expect(rendered[0]).To(Equal("JVM metrics Java 25.0.2 · Mac OS X 26.5.2 · 12 cpus · load 7.25 · up 4m20s · process cpu 0.0% · 2026-11-16T04:53:48Z"))
		Expect(rendered[len(rendered)-2:]).To(Equal([]string{
			"GC G1 Young Generation 2 in 11ms · G1 Old Generation 0 in 0ms",
			`Connectors "http-nio-8080" currentThreadsBusy=3 maxThreads=200`,
		}))
	})

	It("tabulates each pool with its area and an unbounded max", func() {
		pool := jvmruntime.MemoryPool{Name: "G1 Eden Space", Type: "HEAP", Used: 20971520, Committed: 29360128, Max: -1}
		var keys []string
		for _, column := range pool.Columns() {
			keys = append(keys, column.Name)
		}
		Expect(keys).To(Equal([]string{"name", "type", "used", "committed", "max"}))
		row := pool.Row()
		Expect(row["name"]).To(Equal("G1 Eden Space"))
		Expect(row["type"]).To(Equal("heap"))
		Expect(stringOf(row["used"])).To(Equal("20M"))
		Expect(stringOf(row["max"])).To(Equal("unbounded"))
	})

	It("refuses a result that is not a metrics snapshot", func() {
		for _, raw := range []string{`{}`, `{"capturedAt":"2026-11-16T04:53:48Z","memory":{}}`, `[]`} {
			_, err := jvmruntime.DecodeMetrics(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})

func stringOf(value any) string {
	return value.(interface{ String() string }).String()
}
