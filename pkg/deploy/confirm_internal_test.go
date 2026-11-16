package deploy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/agentclient"
)

var _ = Describe("confirming a fresh agent", func() {
	It("resolves its endpoint again on each attempt, so a port-forward torn down before the listener came up is reopened", func() {
		const digest = "provision-1"
		live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "apiVersion": agentclient.AgentAPIVersion, "configDigest": digest})
		}))
		DeferCleanup(live.Close)
		// A local port nobody listens on, as a port-forward is once its first connection was refused.
		closed, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		dead := "http://" + closed.Addr().String()
		Expect(closed.Close()).To(Succeed())

		var resolutions int
		resolve := func() (string, error) {
			resolutions++
			if resolutions == 1 {
				return dead, nil
			}
			return live.URL, nil
		}
		status := confirm(context.Background(), live.Client(), Target{ID: "deployer/orders/shop/orders-0"}, "portforward://orders-0.shop:7070?kind=pod", resolve, digest)
		Expect(status).To(Equal(TargetStatus{State: StateDeployed, AgentAPIVersion: agentclient.AgentAPIVersion, Endpoint: "portforward://orders-0.shop:7070?kind=pod"}))
		Expect(resolutions).To(Equal(2))
	})
})
