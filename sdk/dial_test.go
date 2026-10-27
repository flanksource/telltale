package telltale_test

import (
	"encoding/json"
	"github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strconv"
)

var _ = Describe("dial-out configuration", func() {
	It("rejects a hub URL without a bearer token", func() {
		GinkgoT().Setenv("TELLTALE_TOKEN", "")
		_, err := telltale.Start(telltale.Options{Dial: "http://127.0.0.1:1234", ConfigDir: GinkgoT().TempDir()})
		Expect(err).To(MatchError(ContainSubstring("token")))
	})
	It("generates a local discovery token independently of the hub environment token", func() {
		GinkgoT().Setenv("TELLTALE_URL", "")
		GinkgoT().Setenv("TELLTALE_TOKEN", "hub-secret")
		dir := GinkgoT().TempDir()
		agent, err := telltale.Start(telltale.Options{ConfigDir: dir, Listen: "tcp"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(agent.Close()).To(Succeed()) })
		data, err := os.ReadFile(filepath.Join(dir, "telltale", "agents", strconv.Itoa(os.Getpid())+".json"))
		Expect(err).NotTo(HaveOccurred())
		var endpoint protocol.Endpoint
		Expect(json.Unmarshal(data, &endpoint)).To(Succeed())
		Expect(endpoint.Token).NotTo(Equal("hub-secret"))
		Expect(endpoint.Token).NotTo(BeEmpty())
	})
})
