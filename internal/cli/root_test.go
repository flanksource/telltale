package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/flanksource/telltale/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "CLI") }

// Every spec keeps its trace data and config in temp dirs, never the user's.
var _ = BeforeEach(func() {
	GinkgoT().Setenv("XDG_CACHE_HOME", GinkgoT().TempDir())
	GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
})

// captureStdout is what run printed to os.Stdout, where clicky prints results.
func captureStdout(run func()) string {
	reader, writer, err := os.Pipe()
	Expect(err).ToNot(HaveOccurred())
	stdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = stdout }()
	read := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader) // the pipe is closed below; a short read shows in the assertion
		read <- string(data)
	}()
	run()
	Expect(writer.Close()).To(Succeed())
	return <-read
}

var _ = Describe("command boundaries", func() {
	It("selects an available Vite port by default", func() {
		serve, _, err := cli.NewRoot("dev").Find([]string{"serve"})
		Expect(err).NotTo(HaveOccurred())
		port, err := serve.Flags().GetInt("ui-port")
		Expect(err).NotTo(HaveOccurred())
		Expect(port).To(BeZero())
	})

	It("rejects an invalid top grouping before sampling processes", func() {
		root := cli.NewRoot("dev")
		root.SetArgs([]string{"top", "--group-by", "invalid", "--once"})
		Expect(root.Execute()).To(MatchError(ContainSubstring("group-by")))
	})

	DescribeTable("rejects invalid serve options",
		func(args []string, expected string) {
			root := cli.NewRoot("dev")
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			Expect(root.Execute()).To(MatchError(ContainSubstring(expected)))
		},
		Entry("negative port", []string{"serve", "--port", "-1"}, "port must"),
		Entry("oversized port", []string{"serve", "--port", "65536"}, "port must"),
		Entry("negative Vite port", []string{"serve", "--dev", "--ui-port", "-1"}, "ui-port must"),
		Entry("oversized Vite port", []string{"serve", "--dev", "--ui-port", "65536"}, "ui-port must"),
		Entry("missing source checkout", []string{"serve", "--dev", "--port", "0", "--web-dir", "missing"}, "source checkout"),
	)
	It("prints in the format its flags ask for", func() {
		root := cli.NewRoot("dev")
		root.SetArgs([]string{"trace", "list", "--data-dir", GinkgoT().TempDir(), "--json"})
		output := captureStdout(func() { Expect(root.Execute()).To(Succeed()) })
		Expect(output).To(MatchJSON(`[]`))
	})

	It("reads trace data from the XDG cache dir when --data-dir is not given", func() {
		cache := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CACHE_HOME", cache)
		Expect(os.MkdirAll(filepath.Join(cache, "telltale", "traces", "run-crashed"), 0o755)).To(Succeed())
		root := cli.NewRoot("dev")
		root.SetArgs([]string{"trace", "list", "--json"})
		output := captureStdout(func() { Expect(root.Execute()).To(Succeed()) })
		Expect(output).To(MatchJSON(`[{"runId":"run-crashed","dir":"` + filepath.Join(cache, "telltale", "traces", "run-crashed") + `","status":"unfinished","unavailable":"the run did not finish: it left no run.json"}]`))
	})

	It("reports a supplied version without Git metadata", func() {
		root := cli.NewRoot("dev")
		output := &bytes.Buffer{}
		root.SetOut(output)
		root.SetArgs([]string{"--version"})
		Expect(root.Execute()).To(Succeed())
		Expect(output.String()).To(Equal("telltale version dev\n"))
	})
})
