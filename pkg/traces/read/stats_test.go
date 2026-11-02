package read_test

import (
	"fmt"
	"iter"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sequence(rows ...query.Row) iter.Seq2[query.Row, error] {
	return func(yield func(query.Row, error) bool) {
		for _, row := range rows {
			if !yield(row, nil) {
				return
			}
		}
	}
}

var _ = Describe("bounded statistics", func() {
	It("counts rows when grouping without numeric columns", func() {
		stats, err := read.Stats(sequence(query.Row{"pid": 1}, query.Row{"pid": 1}, query.Row{"pid": 2}), nil, "pid")
		Expect(err).NotTo(HaveOccurred())
		Expect(stats).To(Equal([]query.Row{{"pid": 1, "count": int64(2)}, {"pid": 2, "count": int64(1)}}))
	})
	It("computes nearest-rank percentiles and ignores nulls per group", func() {
		rows := []query.Row{{"pid": 1, "cpu": nil}, {"pid": 2, "cpu": nil}}
		for i := 1; i <= 100; i++ {
			rows = append(rows, query.Row{"pid": 1, "cpu": float64(i)})
		}
		stats, err := read.Stats(sequence(rows...), []string{"cpu"}, "pid")
		Expect(err).NotTo(HaveOccurred())
		Expect(stats).To(Equal([]query.Row{
			{"pid": 1, "column": "cpu", "count": int64(100), "min": float64(1), "avg": 50.5, "max": float64(100), "p50": float64(50), "p90": float64(90), "p95": float64(95), "p99": float64(99), "approximate": false},
			{"pid": 2, "column": "cpu", "count": int64(0), "min": nil, "avg": nil, "max": nil, "p50": nil, "p90": nil, "p95": nil, "p99": nil, "approximate": false},
		}))
	})
	It("reports sampling past the reservoir bound while retaining exact count and extrema", func() {
		rows := func(yield func(query.Row, error) bool) {
			for i := range 20000 {
				if !yield(query.Row{"n": i}, nil) {
					return
				}
			}
		}
		stats, err := read.Stats(rows, []string{"n"}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(stats).To(ConsistOf(And(HaveKeyWithValue("count", int64(20000)), HaveKeyWithValue("min", float64(0)), HaveKeyWithValue("max", float64(19999)), HaveKeyWithValue("approximate", true))))
	})
	It("refuses missing or nonnumeric columns and propagates input errors", func() {
		for _, row := range []query.Row{{"wrong": 1}, {"n": "text"}} {
			_, err := read.Stats(sequence(row), []string{"n"}, "")
			Expect(err).To(HaveOccurred())
		}
		_, err := read.Stats(func(yield func(query.Row, error) bool) { yield(nil, fmt.Errorf("read failed")) }, []string{"n"}, "")
		Expect(err).To(MatchError("read failed"))
	})
})
