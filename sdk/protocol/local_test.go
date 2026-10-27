package protocol_test

import (
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("local endpoint addresses", func() {
	It("accepts absolute unix sockets and loopback TCP only", func() {
		for address, network := range map[string]string{"/example/agent.sock": "unix", "127.0.0.1:1234": "tcp", "[::1]:1234": "tcp"} {
			got, err := protocol.LocalNetwork(address)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(network))
		}
		for _, address := range []string{"example.com:1234", "192.0.2.1:1234", "relative.sock"} {
			_, err := protocol.LocalNetwork(address)
			Expect(err).To(MatchError(ContainSubstring("non-local")))
		}
	})
})
