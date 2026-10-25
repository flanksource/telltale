package cli_test

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/flanksource/telltale/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCLI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "CLI") }

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
		Entry("invalid Vite port", []string{"serve", "--dev", "--ui-port", "0"}, "ui-port must"),
		Entry("missing source checkout", []string{"serve", "--dev", "--port", "0", "--web-dir", "missing"}, "source checkout"),
	)
	It("prints in the format its flags ask for", func() {
		root := cli.NewRoot("dev")
		root.SetArgs([]string{"trace", "list", "--work-dir", GinkgoT().TempDir(), "--json"})
		output := captureStdout(func() { Expect(root.Execute()).To(Succeed()) })
		Expect(output).To(MatchJSON(`[]`))
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
