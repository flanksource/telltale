package sqltop

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQL Top server metrics", func() {
	It("publishes server gauges on empty polls and derives rates from elapsed server time", func() {
		readings := []serverReading{
			{cpuTicks: &[5]int64{100, 20, 20, 500, 10}, io: ioCounters{reset: serverTime, files: map[fileKey]operations{{}: {reads: 20, writes: 80}}}, bufferUsagePercent: new(75.0)},
			{cpuTicks: &[5]int64{130, 20, 30, 550, 20}, io: ioCounters{reset: serverTime, files: map[fileKey]operations{{}: {reads: 20, writes: 100}}}, bufferUsagePercent: new(80.0)},
			{cpuTicks: &[5]int64{140, 20, 30, 555, 25}, io: ioCounters{reset: serverTime.Add(time.Second), files: map[fileKey]operations{{}: {reads: 30, writes: 100}}}, bufferUsagePercent: new(82.0)},
		}
		index := 0
		capture, err := newCapture(Options{Connection: "connection://example"}, EnginePostgres, func(context.Context, bool) (snapshot, error) {
			at := serverTime.Add(time.Duration(index) * 2500 * time.Millisecond)
			readings[index].io.at = at
			current := snapshot{ServerTime: at, Metrics: &readings[index]}
			index++
			return current, nil
		})
		Expect(err).NotTo(HaveOccurred())
		out := &recorder{}
		for range readings {
			Expect(capture.poll(context.Background(), out)).To(Succeed())
		}
		Expect(out.rows).To(BeEmpty())
		Expect(out.frames).To(HaveLen(3))
		want := []string{
			`{"bufferUsagePercent":75,"unavailable":{"cpuPercent":"Waiting for next sample","readIOPS":"Waiting for next sample","writeIOPS":"Waiting for next sample"}}`,
			`{"cpuPercent":40,"readIOPS":0,"writeIOPS":8,"bufferUsagePercent":80}`,
			`{"cpuPercent":50,"bufferUsagePercent":82,"unavailable":{"readIOPS":"Waiting for next sample","writeIOPS":"Waiting for next sample"}}`,
		}
		for i, frame := range out.frames {
			encoded, err := json.Marshal(frame.Summary["serverMetrics"])
			Expect(err).NotTo(HaveOccurred())
			Expect(encoded).To(MatchJSON(want[i]))
		}
	})

	It("waits for a new baseline when SQL Server files change or a counter decreases", func() {
		initial := ioCounters{files: map[fileKey]operations{{database: 1, file: 1}: {reads: 400, writes: 100}}}
		changed := ioCounters{files: map[fileKey]operations{{database: 1, file: 2}: {reads: 500, writes: 110}}}
		Expect(changed.delta(initial)).To(BeNil())
		changed.files = map[fileKey]operations{{database: 1, file: 1}: {reads: 200, writes: 110}}
		Expect(changed.delta(initial)).To(BeNil())
	})

	It("normalizes SQL Server process CPU across logical CPUs and restarts after an instance restart", func() {
		previous := &serverReading{sqlServerCPU: &sqlServerCPUCounters{at: 10_000, startedAt: 1_000, processMS: 1_500, cpus: 4}}
		current := serverReading{sqlServerCPU: &sqlServerCPUCounters{at: 12_000, startedAt: 1_000, processMS: 3_500, cpus: 4}}
		Expect(current.summary(previous)).To(Equal(map[string]any{cpuPercent: 25.0}))
		current.sqlServerCPU.startedAt = 11_000
		Expect(current.summary(previous)).To(HaveKeyWithValue("unavailable", map[string]string{cpuPercent: awaitingSample}))
	})
})
