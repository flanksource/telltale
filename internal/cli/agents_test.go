package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/flanksource/telltale/internal/cli"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent token command", func() {
	It("prints a credential that verifies against the global config dir", func() {
		dir := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
		output := &bytes.Buffer{}
		root := cli.NewRoot("test")
		root.SetOut(output)
		root.SetArgs([]string{"agents", "token", "create", "agent"})
		Expect(root.Execute()).To(Succeed())
		verifier := apitoken.NewVerifier(agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: filepath.Join(dir, "telltale")}}).Lookup)
		_, err := verifier.Verify(context.Background(), strings.TrimSpace(output.String()))
		Expect(err).NotTo(HaveOccurred())
	})
	It("creates an API-scoped token when requested", func() {
		dir := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
		output := &bytes.Buffer{}
		root := cli.NewRoot("test")
		root.SetOut(output)
		root.SetArgs([]string{"agents", "token", "create", "api", "--scope", "api"})
		Expect(root.Execute()).To(Succeed())
		record, err := apitoken.NewVerifier(agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: filepath.Join(dir, "telltale")}}).Lookup).Verify(context.Background(), strings.TrimSpace(output.String()))
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Scope).To(Equal(apitoken.ScopeAPI))
	})
	It("lists connected agents from the selected server", func() {
		GinkgoT().Setenv("TELLTALE_API_TOKEN", "example-api-token")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer example-api-token"))
			_, _ = w.Write([]byte(`[{"agentId":"remote-agent","resource":{"service.name":"example"}}]`))
		}))
		DeferCleanup(server.Close)
		output := &bytes.Buffer{}
		root := cli.NewRoot("test")
		root.SetOut(output)
		root.SetArgs([]string{"agents", "list", "--server", server.URL})
		Expect(root.Execute()).To(Succeed())
		var groups map[string][]protocol.Hello
		Expect(json.Unmarshal(output.Bytes(), &groups)).To(Succeed())
		Expect(groups["other"]).To(HaveLen(1))
		Expect(groups["other"][0].AgentID).To(Equal("remote-agent"))
	})
})
