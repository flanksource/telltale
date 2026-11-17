package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/deploy/auth"
)

const (
	issuerName = "https://telltale.example/issuer"
	targetID   = "deployer:example/orders/orders-0"
	principal  = "operator@example"
)

func rsaKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).ToNot(HaveOccurred())
	return key
}

// verify parses token with the public keys of jwks, keyed by kid, as an
// agent does: RS256 only, with iss and aud required.
func verify(token string, jwks auth.JWKS) (jwt.MapClaims, string) {
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, key := range jwks.Keys {
			if key.KeyID == kid {
				n, err := base64.RawURLEncoding.DecodeString(key.N)
				Expect(err).ToNot(HaveOccurred())
				e, err := base64.RawURLEncoding.DecodeString(key.E)
				Expect(err).ToNot(HaveOccurred())
				return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
			}
		}
		return nil, jwt.ErrTokenUnverifiable
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(issuerName), jwt.WithAudience(targetID), jwt.WithExpirationRequired())
	Expect(err).ToNot(HaveOccurred())
	return claims, parsed.Header["kid"].(string)
}

var _ = Describe("a generated signing key", func() {
	It("is a PKCS#8 PEM RSA key an issuer signs with", func() {
		data, err := auth.GenerateSigningKey()
		Expect(err).ToNot(HaveOccurred())
		block, _ := pem.Decode(data)
		Expect(block).NotTo(BeNil())
		Expect(block.Type).To(Equal("PRIVATE KEY"))
		key, err := auth.ParsePrivateKey(data)
		Expect(err).ToNot(HaveOccurred())
		Expect(key.N.BitLen()).To(Equal(3072))
		_, err = auth.NewIssuer(issuerName, key)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Issuer", func() {
	It("mints an RS256 token an agent verifies with the published JWKS", func() {
		issuer, err := auth.NewIssuer(issuerName, rsaKey())
		Expect(err).ToNot(HaveOccurred())
		token, err := issuer.Mint(auth.Claims{Audience: targetID, Subject: principal, Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeTrace}}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		claims, kid := verify(token, issuer.JWKS())
		Expect(kid).To(Equal(issuer.JWKS().Keys[0].KeyID))
		Expect(claims).To(HaveKeyWithValue("sub", principal))
		Expect(claims).To(HaveKeyWithValue("scope", "jvm:read jvm:trace"))
		exp, err := claims.GetExpirationTime()
		Expect(err).ToNot(HaveOccurred())
		Expect(exp.Time).To(BeTemporally("~", time.Now().Add(time.Minute), 2*time.Second))
	})

	DescribeTable("refuses a token it should not mint",
		func(claims auth.Claims, ttl time.Duration, message string) {
			issuer, err := auth.NewIssuer(issuerName, rsaKey())
			Expect(err).ToNot(HaveOccurred())
			_, err = issuer.Mint(claims, ttl)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("no audience", auth.Claims{Subject: principal, Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute, "audience"),
		Entry("no subject", auth.Claims{Audience: targetID, Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute, "subject"),
		Entry("no scope", auth.Claims{Audience: targetID, Subject: principal}, time.Minute, "scope"),
		Entry("an unknown scope", auth.Claims{Audience: targetID, Subject: principal, Scopes: []auth.Scope{"jvm:root"}}, time.Minute, `unknown scope "jvm:root"`),
		Entry("a long-lived token", auth.Claims{Audience: targetID, Subject: principal, Scopes: []auth.Scope{auth.ScopeRead}}, auth.MaxTTL+time.Second, "ttl"),
	)

	It("keeps the retired key in the JWKS after a rotation and signs with the new one", func() {
		issuer, err := auth.NewIssuer(issuerName, rsaKey())
		Expect(err).ToNot(HaveOccurred())
		before, err := issuer.Mint(auth.Claims{Audience: targetID, Subject: principal, Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(issuer.Rotate(rsaKey())).To(Succeed())
		after, err := issuer.Mint(auth.Claims{Audience: targetID, Subject: principal, Scopes: []auth.Scope{auth.ScopeRead}}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		jwks := issuer.JWKS()
		Expect(jwks.Keys).To(HaveLen(2))
		_, oldKid := verify(before, jwks)
		_, newKid := verify(after, jwks)
		Expect(newKid).ToNot(Equal(oldKid))
		Expect(jwks.Keys[0].KeyID).To(Equal(newKid), "the signing key is listed first")
	})

	It("serves its JWKS for agents configured with a jwksUrl", func() {
		issuer, err := auth.NewIssuer(issuerName, rsaKey())
		Expect(err).ToNot(HaveOccurred())
		recorder := httptest.NewRecorder()
		issuer.ServeHTTP(recorder, httptest.NewRequest("GET", "/.well-known/jwks.json", nil))
		Expect(recorder.Code).To(Equal(200))
		Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))
		var served auth.JWKS
		Expect(json.Unmarshal(recorder.Body.Bytes(), &served)).To(Succeed())
		Expect(served).To(Equal(issuer.JWKS()))
		Expect(served.Keys[0]).To(And(HaveField("KeyType", "RSA"), HaveField("Algorithm", "RS256"), HaveField("Use", "sig")))
	})

	It("loads a PKCS#8 or PKCS#1 PEM signing key and refuses anything else", func() {
		key := rsaKey()
		pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
		Expect(err).ToNot(HaveOccurred())
		for _, block := range []*pem.Block{{Type: "PRIVATE KEY", Bytes: pkcs8}, {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}} {
			parsed, err := auth.ParsePrivateKey(pem.EncodeToMemory(block))
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.Equal(key)).To(BeTrue())
		}
		_, err = auth.ParsePrivateKey([]byte("not a key"))
		Expect(err).To(MatchError(ContainSubstring("PEM")))
	})
})
