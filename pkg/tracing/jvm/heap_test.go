package jvm_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/jvm"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJVM(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM operation results") }

const agentHistogram = `{"collectedAt":"2026-11-14T10:00:00Z","live":false,"totalInstances":14340,"totalBytes":1751776,"matched":2,
"rows":[{"rank":3,"className":"example.Cache","instances":40,"bytes":1048576},{"rank":4,"className":"example.Order","instances":300,"bytes":7200,"loader":"app"}]}`

var _ = Describe("heap histogram", func() {
	It("decodes the agent's typed histogram", func() {
		histogram, err := jvm.DecodeHeapHistogram(json.RawMessage(agentHistogram))
		Expect(err).NotTo(HaveOccurred())
		Expect(histogram).To(Equal(jvm.HeapHistogram{
			CollectedAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), TotalInstances: 14340, TotalBytes: 1751776, Matched: 2,
			Rows: []jvm.HeapClass{
				{Rank: 3, ClassName: "example.Cache", Instances: 40, Bytes: 1048576},
				{Rank: 4, ClassName: "example.Order", Instances: 300, Bytes: 7200, Loader: "app"},
			},
		}))
	})

	It("refuses a result that is not a histogram rather than reporting an empty heap", func() {
		for _, raw := range []string{`{}`, `{"rows":[],"totalBytes":1}`, `{"collectedAt":"2026-11-14T10:00:00Z","rows":null,"totalBytes":1}`, `[]`} {
			_, err := jvm.DecodeHeapHistogram(json.RawMessage(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})

	It("tabulates each class with its rank, counts and loader", func() {
		row := jvm.HeapClass{Rank: 4, ClassName: "example.Order", Instances: 300, Bytes: 7200, Loader: "app"}
		var keys []string
		for _, column := range row.Columns() {
			keys = append(keys, column.Name)
		}
		Expect(keys).To(Equal([]string{"rank", "className", "instances", "bytes", "loader"}))
		Expect(row.Row()).To(HaveKeyWithValue("className", "example.Order"))
		Expect(row.Row()).To(HaveKeyWithValue("loader", "app"))
	})

	It("refuses a negative top before calling the agent", func() {
		Expect(jvm.HeapHistogramParams{Top: -1}.Validate()).To(HaveOccurred())
		Expect(jvm.HeapHistogramParams{Top: 20, ClassPattern: "com.example.*", Live: true}.Validate()).To(Succeed())
	})
})
