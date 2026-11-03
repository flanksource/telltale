package endpoint

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndpoint(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Endpoint authentication") }

var _ = Describe("endpoint authentication", func() {
	It("isolates endpoints and checks revocation despite cached verification", func() {
		minted, err := apitoken.Mint()
		Expect(err).NotTo(HaveOccurred())
		expires := time.Now().Add(time.Hour)
		record := apitoken.Record{ID: minted.ID, SecretHash: minted.Hash, Scope: apitoken.ScopeProxy, Endpoints: []string{"deploy"}, ExpiresAt: &expires}
		verifier := apitoken.NewVerifier(func(_ context.Context, id string) (apitoken.Record, error) {
			if id != record.ID {
				return apitoken.Record{}, apitoken.ErrUnknown
			}
			return record, nil
		})
		endpoint := Policy{Name: "deploy", Connection: "connection://deployment", Auth: Auth{Mode: "token"}}
		id, err := endpoint.Authorize(context.Background(), minted.Secret.Value(), verifier)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(minted.ID))
		endpoint.Name = "other"
		_, err = endpoint.Authorize(context.Background(), minted.Secret.Value(), verifier)
		Expect(err).To(MatchError(apitoken.ErrScope))
		endpoint.Name = "deploy"
		now := time.Now()
		record.RevokedAt = &now
		_, err = endpoint.Authorize(context.Background(), minted.Secret.Value(), verifier)
		Expect(err).To(MatchError(apitoken.ErrRevoked))
	})
	It("never downgrades token authentication and rejects expiry and wrong scopes", func() {
		minted, err := apitoken.Mint()
		Expect(err).NotTo(HaveOccurred())
		expires := time.Now().Add(-time.Second)
		record := apitoken.Record{ID: minted.ID, SecretHash: minted.Hash, Scope: apitoken.ScopeProxy, Endpoints: []string{"deploy"}, ExpiresAt: &expires}
		verifier := apitoken.NewVerifier(func(context.Context, string) (apitoken.Record, error) { return record, nil })
		p := Policy{Name: "deploy", Connection: "connection://deployment", Auth: Auth{Mode: "token"}}
		_, err = p.Authorize(context.Background(), "", verifier)
		Expect(err).To(HaveOccurred())
		_, err = p.Authorize(context.Background(), minted.Secret.Value(), verifier)
		Expect(err).To(MatchError(apitoken.ErrExpired))
		record.ExpiresAt = nil
		record.Scope = apitoken.ScopeAPI
		_, err = p.Authorize(context.Background(), minted.Secret.Value(), verifier)
		Expect(err).To(MatchError(ContainSubstring(apitoken.ErrScope.Error())))
	})
	It("allows independent passthrough endpoints without a token verifier", func() {
		p := Policy{Name: "public"}
		_, err := p.Authorize(context.Background(), "", nil)
		Expect(err).NotTo(HaveOccurred())
		p.Auth.Mode = "typo"
		Expect(p.Validate()).NotTo(Succeed())
		p.Auth.Mode = "token"
		Expect(p.Validate()).NotTo(Succeed())
	})
	It("accepts native proxy Basic and Bearer credentials", func() {
		for _, header := range []string{"Bearer consumer-token", "Basic " + base64.StdEncoding.EncodeToString([]byte("telltale:consumer-token"))} {
			got, err := HTTPToken(header)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal("consumer-token"))
		}
		_, err := HTTPToken("Basic invalid")
		Expect(err).To(MatchError(apitoken.ErrMalformed))
	})
})
