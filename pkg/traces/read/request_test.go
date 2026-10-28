package read_test

import (
	"net/url"
	"time"

	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("record request mapping", func() {
	It("maps HTTP names and repeated filters into the same scope and query as CLI names", func() {
		values := url.Values{
			"workDir": {"work"}, "run": {"run-one"}, "from": {"2026-10-28T10:00:00Z"}, "until": {"2026-10-28T11:00:00Z"},
			"afterSeq": {"2"}, "toSeq": {"8"}, "filter": {"pid=123", "cpuPercent=>=10"}, "where": {"row.pid == 123"},
			"q": {"worker"}, "limit": {"3"}, "cursor": {"next"},
		}
		scope, q, aggregate, err := read.ParseRequest(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(scope).To(Equal(read.Scope{WorkDir: "work", Run: "run-one"}))
		Expect(q).To(Equal(read.Query{
			From: time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC),
			AfterSeq: 2, ToSeq: 8, Filters: []string{"pid=123", "cpuPercent=>=10"}, Where: "row.pid == 123", Search: "worker", Limit: 3, Cursor: "next",
		}))
		Expect(aggregate).To(Equal(read.Aggregate{}))
		for cli, http := range map[string]string{"work-dir": "workDir", "after-seq": "afterSeq", "to-seq": "toSeq", "query": "q"} {
			values[cli] = values[http]
			delete(values, http)
		}
		cliScope, cliQuery, cliAggregate, err := read.ParseRequest(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(cliScope).To(Equal(scope))
		Expect(cliQuery).To(Equal(q))
		Expect(cliAggregate).To(Equal(aggregate))
	})
	It("reads all rows for aggregates unless limit is explicitly provided", func() {
		values := url.Values{"stats": {"cpuPercent,rssBytes"}, "group-by": {"pid"}}
		_, q, aggregate, err := read.ParseRequest(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.All).To(BeTrue())
		Expect(aggregate).To(Equal(read.Aggregate{Columns: []string{"cpuPercent", "rssBytes"}, GroupBy: "pid"}))
		values.Set("limit", "0")
		_, q, _, err = read.ParseRequest(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.All).To(BeFalse())
	})
	It("uses since for run selection and lets from override only the row window", func() {
		before := time.Now().Add(-2 * time.Hour)
		scope, q, _, err := read.ParseRequest(url.Values{"since": {"2h"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(scope.Since).To(BeTemporally(">=", before))
		Expect(scope.Since).To(Equal(q.From))
		scope, q, _, err = read.ParseRequest(url.Values{"since": {"2026-10-27T00:00:00Z"}, "from": {"2026-10-28T00:00:00Z"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(q.From.Sub(scope.Since)).To(Equal(24 * time.Hour))
	})
	DescribeTable("refuses invalid request combinations", func(values url.Values, message string) {
		_, _, _, err := read.ParseRequest(values)
		Expect(err).To(MatchError(ContainSubstring(message)))
	},
		Entry("two selectors", url.Values{"run": {"one"}, "runs": {"two"}}, "--run and --runs"),
		Entry("follow many", url.Values{"follow": {"1"}, "runs": {"one,two"}}, "single run"),
		Entry("follow cursor", url.Values{"follow": {"true"}, "cursor": {"next"}}, "--follow"),
		Entry("follow stats", url.Values{"follow": {"1"}, "stats": {"cpuPercent"}}, "--follow"),
		Entry("view filters", url.Values{"view": {"totals"}, "filter": {"pid=1"}}, "--view"),
		Entry("view limit", url.Values{"view": {"totals"}, "limit": {"0"}}, "--view"),
		Entry("bad time", url.Values{"from": {"yesterday"}}, "--from"),
		Entry("bad since", url.Values{"since": {"-1h"}}, "positive"),
		Entry("bad CEL", url.Values{"where": {"row.pid +"}}, "CEL"),
		Entry("bad seq", url.Values{"afterSeq": {"two"}}, "afterSeq"),
		Entry("negative limit", url.Values{"limit": {"-1"}}, "limit"),
		Entry("bad bool", url.Values{"follow": {"yes"}}, "follow"),
		Entry("duplicate scalar", url.Values{"limit": {"1", "2"}}, "limit"),
	)
})
