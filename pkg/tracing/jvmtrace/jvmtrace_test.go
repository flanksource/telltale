package jvmtrace_test

import (
	"encoding/json"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestJVMTrace(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM method traces") }

var _ = Describe("method trace contracts", func() {
	It("rejects an empty target list and invalid value limits", func() {
		Expect((jvmtrace.Options{}).Validate()).To(HaveOccurred())
		options := jvmtrace.Options{Targets: []jvmtrace.MethodTarget{{Class: "example.Worker", Method: "compute"}}, Expand: 11}
		Expect(options.Validate()).To(HaveOccurred())
		options.Expand = 3
		Expect(options.Validate()).To(Succeed())
	})
	It("decodes exact frame identities and object graphs from the agent", func() {
		blob := agentclient.Blob{Data: []byte(`[{"ts":1794556800000000000,"probeSeq":17,"kind":"enter","key":"capture","threadId":12,"frame":23,"parent":19,"class":"example.Worker","method":"compute","descriptor":"(I)I","args":[{"kind":"scalar","value":"7"}]}]`)}
		blob.Info.ContentType = "application/json"
		events, err := jvmtrace.Decode(blob, map[string]string{"service.name": "example"})
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Frame).To(Equal(int64(23)))
		Expect(events[0].ProbeSeq).To(Equal(int64(17)))
		Expect(events[0].Parent).To(Equal(int64(19)))
		Expect(events[0].Args).To(MatchJSON(json.RawMessage(`[{"kind":"scalar","value":"7"}]`)))
		Expect(events[0].Resource).To(HaveKeyWithValue("service.name", "example"))
	})
})
