package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/flanksource/clicky"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live capture stderr", func() {
	It("streams styled HTTP records without follow while preserving JSON stdout", func() {
		GinkgoT().Setenv("CLICKY_FORCE_INTERACTIVE", "1")
		GinkgoT().Setenv("TERM", "xterm-256color")
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("COLOR", "true")
		saved := *clicky.Flags
		DeferCleanup(func() { *clicky.Flags = saved; clicky.UseFormatter(saved.FormatOptions) })
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("child-output"))
		}))
		DeferCleanup(upstream.Close)
		var stdout, stderr string
		var err error
		formatted := captureStdout(func() {
			stdout, stderr, err = execute("trace", "run", "--data-dir", GinkgoT().TempDir(), "--format", "json", "--", "curl", "--noproxy", "", "-sS", upstream.URL+"/orders")
		})
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(stderr).To(And(ContainSubstring("GET"), ContainSubstring("/orders"), ContainSubstring("\x1b["), ContainSubstring("child-output")))
		Expect(stdout).To(BeEmpty())
		Expect(json.Valid([]byte(formatted))).To(BeTrue(), formatted)
	})
})
