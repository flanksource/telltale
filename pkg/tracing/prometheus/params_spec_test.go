package prometheus_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

var _ = Describe("prometheus params", func() {
	const endpoint = "http://localhost:8080/metrics"

	DescribeTable("refuses what a scrape could not run",
		func(params prometheus.Options, reason string) {
			Expect(params.Validate()).To(MatchError(ContainSubstring(reason)))
		},
		Entry("no endpoint", prometheus.Options{}, "at least one endpoint"),
		Entry("a relative endpoint", prometheus.Options{Endpoints: []string{"/metrics"}}, "absolute http(s) URL"),
		Entry("a non-http scheme", prometheus.Options{Endpoints: []string{"ftp://localhost/metrics"}}, "absolute http(s) URL"),
		Entry("credentials in the URL", prometheus.Options{Endpoints: []string{"http://user:pass@localhost/metrics"}}, "credentials"),
		Entry("a fragment", prometheus.Options{Endpoints: []string{endpoint + "#top"}}, "fragment"),
		Entry("a repeated query param", prometheus.Options{Endpoints: []string{endpoint + "?module=a&module=b"}}, `query param "module"`),
		Entry("the same endpoint twice", prometheus.Options{Endpoints: []string{endpoint, endpoint}}, "given twice"),
		Entry("an interval that is no duration", prometheus.Options{Endpoints: []string{endpoint}, Interval: "often"}, "not a duration"),
		Entry("an interval under 1s", prometheus.Options{Endpoints: []string{endpoint}, Interval: "500ms"}, "shorter than 1s"),
		Entry("an interval finer than a millisecond", prometheus.Options{Endpoints: []string{endpoint}, Interval: "1.0001s"}, "whole milliseconds"),
		Entry("a timeout over the interval", prometheus.Options{Endpoints: []string{endpoint}, Interval: "2s", Timeout: "3s"}, "longer than the interval"),
		Entry("a reserved label", prometheus.Options{Endpoints: []string{endpoint}, Labels: map[string]string{"__address__": "x"}}, `label "__address__" is reserved`),
		Entry("telltale's own label", prometheus.Options{Endpoints: []string{endpoint}, Labels: map[string]string{"telltale_stream": "x"}}, `label "telltale_stream" is reserved`),
		Entry("an invalid label name", prometheus.Options{Endpoints: []string{endpoint}, Labels: map[string]string{"app-name": "x"}}, `label "app-name"`),
	)

	It("accepts endpoints with a query, an interval, a timeout within it and labels", func() {
		Expect(prometheus.Options{
			Endpoints: []string{endpoint, "https://exporter.example.com:9115/probe?module=http_2xx"},
			Interval:  "5s", Timeout: "5s", Labels: map[string]string{"env": "dev"},
		}.Validate()).To(Succeed())
	})
})
