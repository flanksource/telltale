// Package auth mints the short-lived JWTs an execution presents to a deployed
// agent. Tokens are RS256: the agent side must run on Java 8, which has RSA
// signatures but no Ed25519. The agent verifies them against the issuer's
// JWKS, provisioned inline at deploy time and, when the issuer runtime serves
// it, refreshed from a jwksUrl on an unknown kid.
package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Scope is an access level an agent grants per operation and capture kind.
// The agent's catalog names the scope of every operation and probe; the agent
// is the one table, a minter only asks for what the catalog says.
type Scope string

const (
	ScopeRead       Scope = "jvm:read"
	ScopeTrace      Scope = "jvm:trace"
	ScopeInstrument Scope = "jvm:instrument"
	ScopeEval       Scope = "jvm:eval"
	ScopeWrite      Scope = "jvm:write"
)

// Scopes are every scope an agent knows.
var Scopes = []Scope{ScopeRead, ScopeTrace, ScopeInstrument, ScopeEval, ScopeWrite}

// MaxTTL bounds a token's lifetime.
const MaxTTL = 5 * time.Minute

// minKeyBits is the smallest RSA key an issuer signs with.
const minKeyBits = 2048

// Claims are what one execution request is allowed to do.
type Claims struct {
	Audience string
	Subject  string
	Scopes   []Scope
}

func (c Claims) validate(ttl time.Duration) error {
	switch {
	case c.Audience == "":
		return errors.New("mint token: audience (the target id) is required")
	case c.Subject == "":
		return errors.New("mint token: subject (the principal) is required")
	case len(c.Scopes) == 0:
		return errors.New("mint token: at least one scope is required")
	case ttl <= 0 || ttl > MaxTTL:
		return fmt.Errorf("mint token: ttl %s must be positive and at most %s", ttl, MaxTTL)
	}
	for _, scope := range c.Scopes {
		if !slices.Contains(Scopes, scope) {
			return fmt.Errorf("mint token: unknown scope %q", scope)
		}
	}
	return nil
}

// JWK is an RSA public key in JWKS form.
type JWK struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	N         string `json:"n"`
	E         string `json:"e"`
}

// JWKS is the public half of an issuer's keys, the signing key first.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

type signingKey struct {
	private *rsa.PrivateKey
	public  JWK
}

// Issuer signs tokens with its current key and publishes every key it has
// signed with, so tokens minted before a rotation stay verifiable.
type Issuer struct {
	name string
	mu   sync.RWMutex
	keys []signingKey
}

// NewIssuer is an issuer named name (the tokens' iss) signing with key.
func NewIssuer(name string, key *rsa.PrivateKey) (*Issuer, error) {
	if name == "" {
		return nil, errors.New("issuer name is required")
	}
	signing, err := newSigningKey(key)
	if err != nil {
		return nil, err
	}
	return &Issuer{name: name, keys: []signingKey{signing}}, nil
}

func newSigningKey(key *rsa.PrivateKey) (signingKey, error) {
	if key == nil {
		return signingKey{}, errors.New("issuer signing key is required")
	}
	if key.N.BitLen() < minKeyBits {
		return signingKey{}, fmt.Errorf("issuer signing key has %d bits, want at least %d", key.N.BitLen(), minKeyBits)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	return signingKey{private: key, public: JWK{KeyType: "RSA", KeyID: thumbprint(n, e), Use: "sig", Algorithm: "RS256", N: n, E: e}}, nil
}

// thumbprint is the RFC 7638 JWK thumbprint, the key's id.
func thumbprint(n, e string) string {
	sum := sha256.Sum256([]byte(`{"e":"` + e + `","kty":"RSA","n":"` + n + `"}`))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Name is the issuer's iss claim.
func (i *Issuer) Name() string { return i.name }

// Mint signs claims valid for ttl.
func (i *Issuer) Mint(claims Claims, ttl time.Duration) (string, error) {
	if err := claims.validate(ttl); err != nil {
		return "", err
	}
	now := time.Now()
	scopes := make([]string, len(claims.Scopes))
	for index, scope := range claims.Scopes {
		scopes[index] = string(scope)
	}
	i.mu.RLock()
	current := i.keys[0]
	i.mu.RUnlock()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   i.name,
		"aud":   claims.Audience,
		"sub":   claims.Subject,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
		"scope": strings.Join(scopes, " "),
	})
	token.Header["kid"] = current.public.KeyID
	signed, err := token.SignedString(current.private)
	if err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	return signed, nil
}

// Rotate makes key the signing key; the previous keys stay published.
func (i *Issuer) Rotate(key *rsa.PrivateKey) error {
	signing, err := newSigningKey(key)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys = append([]signingKey{signing}, i.keys...)
	return nil
}

// JWKS is every published public key, the signing key first.
func (i *Issuer) JWKS() JWKS {
	i.mu.RLock()
	defer i.mu.RUnlock()
	keys := make([]JWK, len(i.keys))
	for index, key := range i.keys {
		keys[index] = key.public
	}
	return JWKS{Keys: keys}
}

// ServeHTTP serves the JWKS, the endpoint an agent's jwksUrl names.
func (i *Issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=60")
	_ = json.NewEncoder(w).Encode(i.JWKS())
}

// GenerateSigningKey is a new 3072-bit RSA key as PKCS#8 PEM, the form
// ParsePrivateKey reads.
func GenerateSigningKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKey reads a PKCS#8 or PKCS#1 PEM RSA private key.
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("signing key: no PEM block")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signing key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signing key: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("signing key: want an RSA key for RS256, got %T", parsed)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("signing key: unsupported PEM block %q", block.Type)
	}
}
