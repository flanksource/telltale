package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const nativeTelemetryFixture = `{"capturedAt":"2026-11-16T15:00:00Z","applications":[{"loader":"loader-a","label":"PASJava","status":{"armed":true,"mode":"boot","seriesCount":1,"errors":0,"abandonedFrames":0,"overflow":0},"histograms":[{"name":"oipa_activity_processing_seconds","labels":{"transaction":"Premium </script><script>alert(1)</script>","outcome":"success","token":"private-label-value"},"count":9007199254740993,"sumSeconds":0.025,"buckets":{"0.005":0,"0.025":1,"+Inf":9007199254740993}}]}]}`

var _ = Describe("native activity telemetry snapshot", func() {
	It("archives the read-only agent data and HTML with the same inline JSON", func() {
		var called []string
		result, err := collect(context.Background(), Options{}, WorkloadTarget{}, func(_ context.Context, op string) (json.RawMessage, error) {
			called = append(called, op)
			if op == "jvm.telemetry" {
				return json.RawMessage(nativeTelemetryFixture), nil
			}
			if op == "jvm.flags" || op == "jvm.threads.dump" {
				return json.RawMessage(`"diagnostic text"`), nil
			}
			return json.RawMessage(`{}`), nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(ContainElement("jvm.telemetry"))
		Expect(result.Manifest.Complete).To(BeTrue(), "%+v", result.Manifest.Collectors)
		Expect(result.Files).To(HaveKey("telemetry.json"))
		Expect(result.Files).To(HaveKey("telemetry.html"))
		data, page := string(result.Files["telemetry.json"]), string(result.Files["telemetry.html"])
		Expect(data).To(ContainSubstring("9007199254740993"))
		Expect(data).NotTo(ContainSubstring("private-label-value"))
		Expect(page).NotTo(ContainSubstring("private-label-value"))
		Expect(strings.Count(page, "</script>")).To(Equal(1))
		Expect(page).To(ContainSubstring("PASJava"))
		Expect(page).To(ContainSubstring("9007199254740993"))
		island := strings.SplitN(strings.SplitN(page, `id="telemetry-data">`, 2)[1], "</script>", 2)[0]
		Expect(island).To(MatchJSON(data))
		Expect(island).To(ContainSubstring("9007199254740993"))
	})

	It("keeps a partial archive and explicit failures when telemetry cannot be read", func() {
		result, err := collect(context.Background(), Options{}, WorkloadTarget{}, func(_ context.Context, op string) (json.RawMessage, error) {
			if op == "jvm.telemetry" {
				return nil, errors.New("native snapshot API is missing")
			}
			return json.RawMessage(`{}`), nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Manifest.Complete).To(BeFalse())
		Expect(result.Files).NotTo(HaveKey("telemetry.html"))
		Expect(string(result.Files["summary.md"])).To(ContainSubstring("native snapshot API is missing"))
		Expect(result.Files).To(HaveKey("metrics.json"))
	})

	It("opens without a server or external assets when no telemetry is loaded", func() {
		page, err := telemetryHTML([]byte(`{"capturedAt":"2026-11-16T15:00:00Z","applications":[]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(page)).To(ContainSubstring("No loaded activity telemetry"))
		Expect(string(page)).NotTo(ContainSubstring("src="))
		Expect(string(page)).NotTo(ContainSubstring("href="))
	})

	DescribeTable("rejects invalid telemetry instead of rendering invented measurements", func(data string) {
		_, err := telemetryHTML([]byte(data))
		Expect(err).To(HaveOccurred())
	}, Entry("missing applications", `{"capturedAt":"2026-11-16T15:00:00Z"}`),
		Entry("null applications", `{"capturedAt":"2026-11-16T15:00:00Z","applications":null}`),
		Entry("missing capture time", `{"applications":[]}`),
		Entry("infinity differs from count", strings.Replace(nativeTelemetryFixture, `"+Inf":9007199254740993`, `"+Inf":1`, 1)),
		Entry("negative sum", strings.Replace(nativeTelemetryFixture, `"sumSeconds":0.025`, `"sumSeconds":-0.025`, 1)),
		Entry("decreasing cumulative buckets", strings.Replace(nativeTelemetryFixture, `"0.005":0`, `"0.005":2`, 1)),
		Entry("invalid boundary", strings.Replace(nativeTelemetryFixture, `"0.005":0`, `"unknown":0`, 1)))
})
