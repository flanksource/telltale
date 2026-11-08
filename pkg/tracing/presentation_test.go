package tracing_test

import (
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type bodyRecord struct {
	Nested struct {
		Body string `json:"body"`
	} `json:"nested"`
}
type bodyPresenter struct{}

func (bodyPresenter) Params() struct{} { return struct{}{} }
func (bodyPresenter) Schema() recordresults.ResultType[bodyRecord] {
	return recordresults.ResultType[bodyRecord]{Title: "JSON bodies"}
}
func (bodyPresenter) Handle(dbcontext.Context, struct{}, tracing.Emitter[bodyRecord], tracing.Records[bodyRecord]) error {
	return nil
}

var _ = Describe("record presentation", func() {
	It("ranks profile functions by weight and breaks ties by name", func() {
		Expect(tracing.RankedValues(map[string]int64{"z": 1, "b": 4, "a": 4})).To(Equal([]map[string]any{{"function": "a", "value": int64(4)}, {"function": "b", "value": int64(4)}, {"function": "z", "value": int64(1)}}))
	})
	It("restores nested JSON text without changing the native stored payload", func() {
		kinds := tracing.NewKinds()
		plugin := tracing.NewHandler[struct{}, bodyRecord](bodyPresenter{}, tracing.Capabilities{Live: true}, target.Self).WithJSONProcessor("nested.body")
		Expect(kinds.RegisterKind("json_body", plugin)).To(Succeed())
		row := query.Row{"nested": map[string]any{"body": map[string]any{"answer": int64(7)}}}
		presentation, err := kinds.Present("json_body", row)
		Expect(err).NotTo(HaveOccurred())
		Expect(presentation.Detail).NotTo(BeNil())
		Expect(row).To(Equal(query.Row{"nested": map[string]any{"body": map[string]any{"answer": int64(7)}}}))
	})
})
