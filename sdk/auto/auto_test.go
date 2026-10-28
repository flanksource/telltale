package auto_test

import (
	"net/http"
	"os"
	"os/exec"
	"testing"

	_ "github.com/flanksource/telltale/sdk/auto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAuto(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Auto agent") }

var _ = Describe("automatic startup", func() {
	It("logs startup failure without terminating the host", func() {
		if os.Getenv("TELLTALE_AUTO_TEST_CHILD") == "1" {
			_, ok := http.DefaultTransport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(http.DefaultClient.Transport).NotTo(BeNil())
			return
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestAuto", "-ginkgo.focus=logs startup failure")
		cmd.Env = append(os.Environ(), "TELLTALE_AUTO_TEST_CHILD=1", "TELLTALE_URL=invalid-url", "TELLTALE_TOKEN=example")
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("telltale agent startup failed"))
	})
})
