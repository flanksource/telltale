package prometheus_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/prometheus"
)

var _ = Describe("prometheus params", func() {
	const endpoint = "http://localhost:8080/metrics"

	DescribeTable("refuses what a scrape could not run",
		func(params prometheus.Params, reason string) {
			Expect(params.Validate()).To(MatchError(ContainSubstring(reason)))
		},
		Entry("no endpoint", prometheus.Params{}, "at least one endpoint"),
		Entry("a relative endpoint", prometheus.Params{Endpoints: []string{"/metrics"}}, "absolute http(s) URL"),
		Entry("a non-http scheme", prometheus.Params{Endpoints: []string{"ftp://localhost/metrics"}}, "absolute http(s) URL"),
		Entry("credentials in the URL", prometheus.Params{Endpoints: []string{"http://user:pass@localhost/metrics"}}, "credentials"),
		Entry("a fragment", prometheus.Params{Endpoints: []string{endpoint + "#top"}}, "fragment"),
		Entry("a repeated query param", prometheus.Params{Endpoints: []string{endpoint + "?module=a&module=b"}}, `query param "module"`),
		Entry("the same endpoint twice", prometheus.Params{Endpoints: []string{endpoint, endpoint}}, "given twice"),
		Entry("an interval that is no duration", prometheus.Params{Endpoints: []string{endpoint}, Interval: "often"}, "not a duration"),
		Entry("an interval under 1s", prometheus.Params{Endpoints: []string{endpoint}, Interval: "500ms"}, "shorter than 1s"),
		Entry("an interval finer than a millisecond", prometheus.Params{Endpoints: []string{endpoint}, Interval: "1.0001s"}, "whole milliseconds"),
		Entry("a timeout over the interval", prometheus.Params{Endpoints: []string{endpoint}, Interval: "2s", Timeout: "3s"}, "longer than the interval"),
		Entry("a reserved label", prometheus.Params{Endpoints: []string{endpoint}, Labels: map[string]string{"__address__": "x"}}, `label "__address__" is reserved`),
		Entry("telltale's own label", prometheus.Params{Endpoints: []string{endpoint}, Labels: map[string]string{"telltale_stream": "x"}}, `label "telltale_stream" is reserved`),
		Entry("an invalid label name", prometheus.Params{Endpoints: []string{endpoint}, Labels: map[string]string{"app-name": "x"}}, `label "app-name"`),
	)

	It("accepts endpoints with a query, an interval, a timeout within it and labels", func() {
		Expect(prometheus.Params{
			Endpoints: []string{endpoint, "https://exporter.example.com:9115/probe?module=http_2xx"},
			Interval:  "5s", Timeout: "5s", Labels: map[string]string{"env": "dev"},
		}.Validate()).To(Succeed())
	})
})
