package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/flanksource/clicky"
	"github.com/flanksource/telltale"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("styled replay output", func() {
	It("keeps HTTP pretty-printing colors and honors no-color", func() {
		GinkgoT().Setenv("CLICKY_FORCE_INTERACTIVE", "1")
		GinkgoT().Setenv("TERM", "xterm-256color")
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("COLOR", "true")
		saved := *clicky.Flags
		DeferCleanup(func() { *clicky.Flags = saved; clicky.UseFormatter(saved.FormatOptions) })
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("recorded")) }))
		DeferCleanup(server.Close)
		dataDir := GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{DataDir: dataDir, Command: []string{"curl", "--noproxy", "", "-sS", server.URL + "/orders"},
			Traces: []telltale.TraceSpec{{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)}}})
		Expect(err).NotTo(HaveOccurred())
		_, err = run.Wait()
		Expect(err).NotTo(HaveOccurred())
		stdout, stderr, err := execute("trace", "replay", run.ID(), "--data-dir", dataDir, "--format", "ansi")
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(clicky.Flags.NoColor).To(BeFalse(), "explicit color environment should keep colors")
		Expect(stdout).To(ContainSubstring("\x1b["))
		Expect(stdout).To(ContainSubstring("GET"))
		stdout, stderr, err = execute("trace", "replay", run.ID(), "--data-dir", dataDir, "--format", "ansi", "--no-color")
		Expect(err).NotTo(HaveOccurred(), stderr)
		Expect(stdout).NotTo(ContainSubstring("\x1b["))
		Expect(stdout).To(ContainSubstring("GET"))
	})
})
