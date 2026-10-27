package exec

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clicky "github.com/flanksource/telltale/sdk/clicky"
)

var _ = Describe("an exec record emitted by an agent", func() {
	It("has secrets passed as separate arguments masked again before it is stored", func() {
		record := ExecRecord{Record: clicky.Record{Name: "mysql", Args: []string{"--password", "hunter2", "-p", "private", "--port", "3306"}}}
		Expect(maskRecordArgs(record).Args).To(Equal([]string{"--password", "****", "-p", "****", "--port", "3306"}))
	})
})
