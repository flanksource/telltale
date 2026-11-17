package agentclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/agentclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent inventory HTTP API", func() {
	It("serves saved targets with the same connection state and namespace group as the CLI", func() {
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(GinkgoT().TempDir(), "no-local-agents")
		registry.SetLister("deployer/", func(context.Context) ([]agentclient.Known, error) {
			return []agentclient.Known{{ID: "deployer/orders/shop/orders-0", Runtime: "java", Names: []string{"orders"}, State: "deployed",
				Resource: map[string]string{"k8s.namespace.name": "shop"}, Endpoint: "http://orders-0:7070"}}, nil
		})
		hub := agentclient.NewHub(registry, agentclient.NewTokenStore(nil))
		response := httptest.NewRecorder()
		hub.List(response, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
		Expect(response.Code).To(Equal(http.StatusOK))
		var infos []agentclient.Info
		Expect(json.Unmarshal(response.Body.Bytes(), &infos)).To(Succeed())
		Expect(infos).To(ConsistOf(And(HaveField("AgentID", "deployer/orders/shop/orders-0"),
			HaveField("Group", "namespace/shop"), HaveField("Connected", false), HaveField("Known.State", "deployed"))))
	})
})
