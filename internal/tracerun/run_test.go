package tracerun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flanksource/telltale/internal/tracerun"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTracerun(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "tracerun") }

var _ = Describe("parsing a trace spec", func() {
	DescribeTable("reads kind or kind:{json}",
		func(value string, expected tracerun.Spec) {
			Expect(tracerun.ParseSpec(value)).To(Equal(expected))
		},
		Entry("bare kind", "http_proxy", tracerun.Spec{Kind: "http_proxy"}),
		Entry("kind with params", `http_proxy:{"mode":"mitm"}`, tracerun.Spec{Kind: "http_proxy", Params: json.RawMessage(`{"mode":"mitm"}`)}),
	)
	DescribeTable("refuses",
		func(value, expected string) {
			_, err := tracerun.ParseSpec(value)
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("no kind", `:{"mode":"mitm"}`, "names no kind"),
		Entry("params that are not JSON", "http_proxy:mode=mitm", "not JSON"),
	)
})

var _ = Describe("running a command under trace", func() {
	run := func(command []string, specs ...tracerun.Spec) (*tracerun.Result, string) {
		output := &bytes.Buffer{}
		result, err := tracerun.Run(context.Background(), tracerun.Options{
			WorkDir: GinkgoT().TempDir(), Command: command, Traces: specs, Stdout: output, Stderr: output,
		})
		Expect(err).ToNot(HaveOccurred(), output.String())
		return result, output.String()
	}

	It("reports the command's exit code and the process tree it recorded", func() {
		result, _ := run([]string{"sh", "-c", "exit 3"})
		Expect(result.ExitCode).To(Equal(3))
		Expect(result.Dir).To(BeADirectory())
		Expect(result.Process.Tree.Kind).To(Equal(process.TreeKind))
		Expect(result.Captures).To(BeEmpty())
	})

	It("points the command at a recording proxy and summarizes the exchanges it relayed", func() {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("traced"))
		}))
		DeferCleanup(upstream.Close)

		result, output := run([]string{"curl", "-sS", upstream.URL + "/hello"}, tracerun.Spec{Kind: httpproxy.Kind})
		Expect(result.ExitCode).To(Equal(0), output)
		Expect(output).To(Equal("traced"))
		Expect(result.Captures).To(HaveLen(1))
		Expect(result.Captures[0].Kind).To(Equal(httpproxy.Kind))
		Expect(result.Captures[0].Summary).To(HaveKeyWithValue("count", BeNumerically("==", 1)))
	})

	It("refuses a kind the catalog does not serve before running anything", func() {
		_, err := tracerun.Run(context.Background(), tracerun.Options{
			WorkDir: GinkgoT().TempDir(), Command: []string{"true"}, Traces: []tracerun.Spec{{Kind: "nosuchkind"}},
		})
		Expect(err).To(MatchError(ContainSubstring(`unknown trace kind "nosuchkind"`)))
	})
})
