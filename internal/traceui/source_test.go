package traceui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/telltale"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("trace terminal output", func() {
	It("pages large saved output without retaining the whole file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "stdout.log")
		Expect(os.WriteFile(path, []byte("first\n"+strings.Repeat("x", 2<<20)+"\nlast"), 0600)).To(Succeed())
		first, err := outputWindow(path, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(ContainSubstring("first\n"))
		Expect(len(first)).To(BeNumerically("<", (1<<20)+256))
		last, err := outputWindow(path, -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(last).To(HaveSuffix("\nlast"))
	})
	It("keeps stdout and stderr separate including partial lines", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, telltale.StdoutFile), []byte("standard\npartial"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, telltale.StderrFile), []byte("diagnostic"), 0600)).To(Succeed())
		m := New(context.Background(), Options{Runs: []telltale.RunInfo{{Dir: dir, Output: &telltale.OutputRefs{Stdout: telltale.StdoutFile, Stderr: telltale.StderrFile}}}})
		out, err := m.readOutput("stdout")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("standard\npartial"))
		errout, err := m.readOutput("stderr")
		Expect(err).NotTo(HaveOccurred())
		Expect(errout).To(Equal("diagnostic"))
	})
	It("rejects a missing declared output file", func() {
		m := New(context.Background(), Options{Runs: []telltale.RunInfo{{Dir: GinkgoT().TempDir(), Output: &telltale.OutputRefs{Stdout: telltale.StdoutFile}}}})
		_, err := m.readOutput("stdout")
		Expect(err).To(HaveOccurred())
	})
	It("bounds every rendered line and keeps the footer on small terminals", func() {
		m := New(context.Background(), Options{})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
		m = updated.(Model)
		m.tabs[0].output = strings.Repeat("long output ", 100)
		lines := strings.Split(m.View().Content, "\n")
		Expect(lines).To(HaveLen(8))
		for _, line := range lines {
			Expect(ansi.StringWidth(line)).To(BeNumerically("<=", 40))
		}
	})
})
