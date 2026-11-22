package groups

import (
	"os"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("live compressed memory", func() {
	ginkgo.It("reads the current process's compressed memory through its Mach task", func() {
		var reading Reading
		readCompressedMemory(os.Getpid(), &reading)
		Expect(reading.CompressedBytes).NotTo(BeNil())
		Expect(reading.MemoryError).To(BeEmpty())
	})
})
