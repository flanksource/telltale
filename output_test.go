package telltale_test

import (
	"context"
	"github.com/flanksource/telltale"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = Describe("recorded process output", func() {
	It("persists separate streams in running and completed metadata", func() {
		run, err := telltale.Start(context.Background(), telltale.StartOptions{DataDir: GinkgoT().TempDir(), Command: []string{"sh", "-c", "printf standard\nprintf diagnostic >&2"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(run.Info().Output).To(Equal(&telltale.OutputRefs{Stdout: telltale.StdoutFile, Stderr: telltale.StderrFile}))
		result, err := run.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ExitCode).To(BeZero())
		for name, want := range map[string]string{telltale.StdoutFile: "standard", telltale.StderrFile: "diagnostic"} {
			data, err := os.ReadFile(filepath.Join(run.Dir(), name))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(want))
		}
		info, err := telltale.Get(telltale.GetOptions{DataDir: filepath.Dir(filepath.Dir(run.Dir())), RunID: run.ID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Output).To(Equal(result.Output))
	})
})
