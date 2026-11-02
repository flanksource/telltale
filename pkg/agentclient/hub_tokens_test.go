package agentclient_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("hub tokens", func() {
	var cwd, global string
	var dirs []datadir.ConfigDir

	BeforeEach(func() {
		cwd, global = GinkgoT().TempDir(), GinkgoT().TempDir()
		dirs = []datadir.ConfigDir{{Name: datadir.ConfigCWD, Dir: cwd}, {Name: datadir.ConfigGlobal, Dir: global}}
	})

	It("mints into the global config dir, persisting only the hash, and verifies after reopening", func() {
		minted, err := agentclient.NewTokenStore(dirs).Mint("agent", "agent")
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(cwd, "agent-tokens")).ToNot(BeADirectory())
		data, err := os.ReadFile(filepath.Join(global, "agent-tokens", minted.ID+".json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring(minted.Secret.Value()))
		Expect(string(data)).To(ContainSubstring(minted.Hash))
		verifier := apitoken.NewVerifier(agentclient.NewTokenStore(dirs).Lookup)
		record, err := verifier.Verify(context.Background(), minted.Secret.Value())
		Expect(err).NotTo(HaveOccurred())
		Expect(record.ID).To(Equal(minted.ID))
		_, err = verifier.Verify(context.Background(), strings.Replace(minted.Secret.Value(), ".", ".wrong", 1))
		Expect(err).To(HaveOccurred())
	})

	It("looks a token up in the working directory's config before the global one", func() {
		minted, err := agentclient.NewTokenStore(dirs).Mint("global-name", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		record, err := agentclient.NewTokenStore(dirs).Lookup(context.Background(), minted.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Name).To(Equal("global-name"))

		record.Name = "cwd-name"
		data, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(filepath.Join(cwd, "agent-tokens"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cwd, "agent-tokens", minted.ID+".json"), data, 0o600)).To(Succeed())
		Expect(agentclient.NewTokenStore(dirs).Lookup(context.Background(), minted.ID)).To(HaveField("Name", "cwd-name"))
	})

	It("refuses to mint without a global config dir", func() {
		_, err := agentclient.NewTokenStore(dirs[:1]).Mint("agent", apitoken.ScopeAgent)
		Expect(err).To(MatchError(ContainSubstring("global")))
	})
})
