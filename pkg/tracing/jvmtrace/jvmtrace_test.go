package jvmtrace_test

import (
	"encoding/json"
	"testing"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJVMTrace(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM method traces") }

var worker = []jvmtrace.MethodTarget{{Class: "example.Worker", Method: "compute"}}

var _ = Describe("method trace contracts", func() {
	It("rejects an empty target list and invalid value limits", func() {
		Expect((jvmtrace.Options{}).Validate()).To(HaveOccurred())
		options := jvmtrace.Options{Targets: worker, Expand: 11}
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

var _ = Describe("root-call filters", func() {
	DescribeTable("refuses a filter the agent could not evaluate",
		func(options jvmtrace.Options, reason string) {
			options.Targets = worker
			Expect(options.Validate()).To(MatchError(ContainSubstring(reason)))
		},
		Entry("an unparseable minCost", jvmtrace.Options{MinCost: "fast"}, "minCost"),
		Entry("a negative minCost", jvmtrace.Options{MinCost: "-1s"}, "minCost"),
		Entry("an unknown operator", jvmtrace.Options{Args: true, Match: []jvmtrace.ValueMatch{{On: "arg", Op: "startsWith", Value: "x"}}}, "startsWith"),
		Entry("an unknown subject", jvmtrace.Options{Args: true, Match: []jvmtrace.ValueMatch{{On: "this", Op: "equals", Value: "x"}}}, "arg or return"),
		Entry("an argument that is not recorded", jvmtrace.Options{Match: []jvmtrace.ValueMatch{{On: "arg", Op: "equals", Value: "x"}}}, "args"),
		Entry("a return value that is not recorded", jvmtrace.Options{Match: []jvmtrace.ValueMatch{{On: "return", Op: "equals", Value: "x"}}}, "return"),
		Entry("an index on a return value", jvmtrace.Options{Return: true, Match: []jvmtrace.ValueMatch{{On: "return", Index: 1, Op: "equals", Value: "x"}}}, "index"),
		Entry("a negative argument index", jvmtrace.Options{Args: true, Match: []jvmtrace.ValueMatch{{On: "arg", Index: -1, Op: "equals", Value: "x"}}}, "index"),
	)

	It("accepts a filter whose every predicate names a recorded value", func() {
		options := jvmtrace.Options{Targets: worker, Args: true, Return: true, MinCost: "250ms", ExceptionsOnly: true, Match: []jvmtrace.ValueMatch{
			{On: "arg", Index: 1, Path: "customer.name", Op: "regex", Value: "^acme-[0-9]+$"},
			{On: "return", Op: "contains", Value: "sent"},
		}}
		Expect(options.Validate()).To(Succeed())
		wire, err := json.Marshal(options)
		Expect(err).NotTo(HaveOccurred())
		Expect(wire).To(MatchJSON(`{"targets":[{"class":"example.Worker","method":"compute"}],"args":true,"return":true,"minCost":"250ms","exceptionsOnly":true,"match":[{"on":"arg","index":1,"path":"customer.name","op":"regex","value":"^acme-[0-9]+$"},{"on":"return","index":0,"op":"contains","value":"sent"}]}`))
	})

	DescribeTable("checks the agent's probe API before arming",
		func(options jvmtrace.Options, version int, refused bool) {
			err := options.CompatibleWith(protocol.ProbeSpec{Name: jvmtrace.Kind, Version: version})
			if refused {
				Expect(err).To(MatchError(And(ContainSubstring("v10"), ContainSubstring("restart the JVM"))))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("filters against an agent that predates them", jvmtrace.Options{MinCost: "1ms"}, 9, true),
		Entry("filters against an agent that reports no version", jvmtrace.Options{ExceptionsOnly: true}, 0, true),
		Entry("filters against a current agent", jvmtrace.Options{MinCost: "1ms"}, 10, false),
		Entry("no filters against an old agent", jvmtrace.Options{Args: true}, 0, false),
	)

	It("decodes how many events a kept call omitted", func() {
		blob := agentclient.Blob{Data: []byte(`[{"ts":1,"kind":"throw","key":"k","threadId":1,"frame":1,"omitted":7}]`)}
		blob.Info.ContentType = "application/json"
		events, err := jvmtrace.Decode(blob, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(events[0].Omitted).To(Equal(7))
	})
})
