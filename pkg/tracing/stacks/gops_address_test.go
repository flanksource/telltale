package stacks_test

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/tracing/stacks"
)

var _ = Describe("the gops agent address", func() {
	const port = "51234"
	var dir, portFile string
	pid := os.Getpid()

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		portFile = filepath.Join(dir, strconv.Itoa(pid))
		GinkgoT().Setenv("GOPS_CONFIG_DIR", dir)
	})

	It("is read from the port file the agent wrote under GOPS_CONFIG_DIR", func() {
		Expect(os.WriteFile(portFile, []byte(port+"\n"), 0o600)).To(Succeed())
		addr, ok := stacks.GopsAddress(pid)
		Expect(ok).To(BeTrue())
		Expect(addr).To(Equal("127.0.0.1:" + port))
	})

	It("is refused for a process that wrote no port file, naming the file looked for", func() {
		_, ok := stacks.GopsAddress(pid)
		Expect(ok).To(BeFalse())
		_, err := stacks.GopsPort(pid)
		Expect(err).To(MatchError(ContainSubstring("runs no gops agent: no " + portFile)))
	})

	It("is refused for a port file an earlier process with the same pid left behind", func() {
		Expect(os.WriteFile(portFile, []byte(port), 0o600)).To(Succeed())
		left := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		Expect(os.Chtimes(portFile, left, left)).To(Succeed())
		_, err := stacks.GopsPort(pid)
		Expect(err).To(MatchError(ContainSubstring("was written at 2001-01-01T00:00:00Z, before the process started")))
	})

	It("is refused for a port file that holds no port", func() {
		Expect(os.WriteFile(portFile, []byte("nope"), 0o600)).To(Succeed())
		_, err := stacks.GopsPort(pid)
		Expect(err).To(MatchError(ContainSubstring(`"nope" is not a port`)))
	})
})
