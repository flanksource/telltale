package snapshot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

func agentFixture(op string) json.RawMessage {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "tracing", "jvm", "jvmruntime", "testdata", op+".json"))
	Expect(err).NotTo(HaveOccurred())
	return data
}

var _ = Describe("workload panel", func() {
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	sample := func(offset time.Duration, cpu float64, memory, eden, gcCount, gcTimeMs int64, live, daemon, blocked int) workloadSample {
		return workloadSample{At: start.Add(offset), CPUCores: cpu, CPULimit: 2, Memory: memory, MemoryLimit: 4000, Eden: eden, Survivor: 10, Old: 200,
			HeapMax: 1000, Metaspace: 50, CodeCache: 20, Direct: 5, GCCount: gcCount, GCTimeMs: gcTimeMs, Live: live, Daemon: daemon, Blocked: blocked}
	}
	series := []workloadSample{
		sample(0, 0.5, 1000, 100, 0, 0, 10, 5, 0),
		sample(5*time.Second, 1.0, 1200, 200, 2, 20, 12, 5, 0),
		sample(10*time.Second, 1.5, 1400, 300, 3, 50, 11, 6, 1),
	}

	It("reads container usage from cgroup v2 and JVM usage from the agent's pools", func() {
		metrics, err := jvmruntime.DecodeMetrics(agentFixture(jvmruntime.MetricsOp))
		Expect(err).NotTo(HaveOccurred())
		resources, err := jvmruntime.DecodeResources(agentFixture(jvmruntime.ResourcesOp))
		Expect(err).NotTo(HaveOccurred())
		threads, err := jvmruntime.DecodeThreads(agentFixture(jvmruntime.ThreadsOp))
		Expect(err).NotTo(HaveOccurred())
		got, err := newWorkloadSample(metrics, resources, threads)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(workloadSample{
			At: metrics.CapturedAt, CPUCores: 0.09098106439764791 / 100 * 12, CPULimit: 2, Memory: 612368384, MemoryLimit: 2147483648,
			Eden: 8388608, Survivor: 17704008, Old: 17925592, HeapMax: 8589934592,
			Metaspace: 55839576, CodeCache: 1739520 + 14589696 + 1571264, Direct: 0,
			GCCount: 15 + 6 + 1, GCTimeMs: 45 + 5 + 21, Live: 11, Daemon: 10, Blocked: 0,
		}))
	})

	It("refuses a JVM that runs outside a cgroup v2 container", func() {
		metrics, err := jvmruntime.DecodeMetrics(agentFixture(jvmruntime.MetricsOp))
		Expect(err).NotTo(HaveOccurred())
		_, err = newWorkloadSample(metrics, jvmruntime.Resources{Linux: map[string]string{"status": "not-applicable"}}, jvmruntime.ThreadSample{})
		Expect(err).To(MatchError(ContainSubstring("no cgroup v2")))
	})

	It("summarises averages, percentiles, per-interval GC pauses and the thread peak", func() {
		panel, err := newWorkloadPanel(WorkloadTarget{Kind: "pod", Name: "api-0", Namespace: "lab", Container: "api"}, series)
		Expect(err).NotTo(HaveOccurred())
		Expect(panel.Workload).To(HaveKeyWithValue("kind", "pod"))
		Expect(panel.Workload).To(HaveKeyWithValue("namespace", "lab"))
		Expect(panel.Container).To(Equal("api"))
		Expect(panel.ObservationWindowMs).To(Equal(int64(10000)))
		Expect(panel.CPU).To(Equal(statistics{Average: 1, P90: 1.5, P95: 1.5, Limit: 2}))
		Expect(panel.Memory).To(Equal(statistics{Average: 1200, P90: 1400, P95: 1400, Limit: 4000}))
		Expect(panel.JVM.Heap).To(Equal(statistics{Average: 410, P90: 510, P95: 510, Limit: 1000}))
		Expect(panel.JVM.Regions).To(Equal(map[string]float64{"eden": 200, "survivor": 10, "old": 200, "metaspace": 50, "codeCache": 20, "direct": 5}))
		// Intervals: 2 collections in 20ms (10ms each, 0.4% of 5s), then 1 in 30ms (0.6%).
		Expect(panel.JVM.GC).To(Equal(gcStatistics{Count: 3, TimeMs: 50, P90Ms: 30, P95Ms: 30, MaxMs: 30, PercentP90: 0.6, PercentP95: 0.6}))
		Expect(panel.JVM.Threads).To(Equal(map[string]int{"live": 11, "daemon": 6, "blocked": 1, "peak": 12}))
	})

	It("refuses JVM regions larger than the container's memory", func() {
		small := []workloadSample{sample(0, 1, 100, 100, 0, 0, 1, 0, 0), sample(time.Second, 1, 100, 100, 0, 0, 1, 0, 0)}
		_, err := newWorkloadPanel(WorkloadTarget{Name: "api", Container: "api"}, small)
		Expect(err).To(MatchError(ContainSubstring("more than the container's")))
	})

	It("refuses GC totals that go backwards", func() {
		restarted := []workloadSample{sample(0, 1, 1000, 100, 5, 50, 1, 0, 0), sample(time.Second, 1, 1000, 100, 1, 5, 1, 0, 0)}
		_, err := newWorkloadPanel(WorkloadTarget{Name: "api", Container: "api"}, restarted)
		Expect(err).To(MatchError(ContainSubstring("went backwards")))
	})

	It("embeds the panel so no value can close its script element", func() {
		panel, err := newWorkloadPanel(WorkloadTarget{Name: "</script><script>alert(1)</script>", Container: "api"}, series)
		Expect(err).NotTo(HaveOccurred())
		page, err := workloadHTML(panel)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(page)).NotTo(ContainSubstring(workloadMarker))
		Expect(strings.Count(string(page), "</script>")).To(Equal(strings.Count(string(workloadPage), "</script>")))
		island := strings.SplitN(strings.SplitN(string(page), `id="workload">`, 2)[1], "</script>", 2)[0]
		var embedded workloadPanel
		Expect(json.Unmarshal([]byte(island), &embedded)).To(Succeed())
		Expect(embedded.Workload["name"]).To(Equal("</script><script>alert(1)</script>"))
	})

	It("names the workload from the agent's Kubernetes resource attributes", func() {
		Expect(workloadTarget(map[string]string{"telltale.deployer": "api", "k8s.namespace.name": "lab", "k8s.container.name": "app", "k8s.pod.name": "api-1"}, "")).
			To(Equal(WorkloadTarget{Name: "api", Namespace: "lab", Container: "app"}))
		Expect(workloadTarget(map[string]string{"k8s.pod.name": "cycle-0"}, "")).To(Equal(WorkloadTarget{Kind: "pod", Name: "cycle-0", Container: "cycle-0"}))
		Expect(workloadTarget(nil, "billing")).To(Equal(WorkloadTarget{Name: "billing", Container: "billing"}))
	})

	It("rejects workload windows outside the sampling interval to 5m", func() {
		Expect((Options{Workload: "1s"}).Validate()).NotTo(Succeed())
		Expect((Options{Workload: "6m"}).Validate()).NotTo(Succeed())
		Expect((Options{Workload: "1m"}).Validate()).To(Succeed())
		Expect((Options{Workload: "1m"}).workloadSamples()).To(Equal(13))
	})

	It("adds workload.json and workload.html to the archive", func() {
		original := workloadInterval
		workloadInterval = time.Millisecond
		DeferCleanup(func() { workloadInterval = original })
		polls := 0
		result, err := collect(context.Background(), Options{Workload: "2ms"}, WorkloadTarget{Name: "api", Container: "api"}, func(_ context.Context, op string) (json.RawMessage, error) {
			switch op {
			case jvmruntime.ResourcesOp, jvmruntime.ThreadsOp:
				return agentFixture(op), nil
			case jvmruntime.MetricsOp:
			default:
				return json.RawMessage(`{}`), nil
			}
			var metrics map[string]any
			Expect(json.Unmarshal(agentFixture(op), &metrics)).To(Succeed())
			polls++
			metrics["capturedAt"] = start.Add(time.Duration(polls) * time.Second)
			return json.Marshal(metrics)
		})
		Expect(err).NotTo(HaveOccurred())
		var samples []workloadSample
		Expect(json.Unmarshal(result.Files["workload.json"], &samples)).To(Succeed())
		Expect(samples).To(HaveLen(3))
		Expect(string(result.Files["workload.html"])).To(ContainSubstring(`"observationWindowMs":2000`))
	})
})
