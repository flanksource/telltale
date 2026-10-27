package agentclient_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("hub tokens", func() {
	It("persists only the hash and verifies a minted credential after reopening", func() {
		workDir := GinkgoT().TempDir()
		store := agentclient.NewTokenStore(workDir)
		minted, err := store.Mint("agent", "agent")
		Expect(err).NotTo(HaveOccurred())
		data, err := os.ReadFile(filepath.Join(workDir, ".telltale", "agent-tokens", minted.ID+".json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring(minted.Secret.Value()))
		Expect(string(data)).To(ContainSubstring(minted.Hash))
		verifier := apitoken.NewVerifier(agentclient.NewTokenStore(workDir).Lookup)
		record, err := verifier.Verify(context.Background(), minted.Secret.Value())
		Expect(err).NotTo(HaveOccurred())
		Expect(record.ID).To(Equal(minted.ID))
		_, err = verifier.Verify(context.Background(), strings.Replace(minted.Secret.Value(), ".", ".wrong", 1))
		Expect(err).To(HaveOccurred())
	})
})
