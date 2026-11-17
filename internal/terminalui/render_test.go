package terminalui

import (
	"github.com/charmbracelet/x/ansi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"testing"
)

func TestTerminalUI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Terminal rendering") }

var _ = Describe("shared rendering", func() {
	It("fits Unicode and ANSI without splitting cells or leaking beyond the viewport", func() {
		for _, width := range []int{0, 1, 5, 20} {
			styled := "\x1b[31m数据库 worker\x1b[0m"
			Expect(ansi.StringWidth(Fit(styled, width))).To(Equal(width))
			Expect(ansi.StringWidth(Clip(styled, width))).To(BeNumerically("<=", width))
		}
	})
	It("bounds stacked meters and fills the remaining cells", func() {
		Expect(ansi.Strip(Bar(10, []Share{{Percent: 25, Style: CPU}, {Percent: 200, Style: Memory}}))).To(Equal("[" + strings.Repeat("█", 10) + "]"))
		Expect(ansi.Strip(Bar(4, nil))).To(Equal("[░░░░]"))
		Expect(PadLines("one\ntwo", 1)).To(Equal([]string{"one"}))
		Expect(PadLines("one", 3)).To(Equal([]string{"one", "", ""}))
	})
})
