package auth

import (
	"context"
	"errors"
	"time"
)

// Tokens mints one token per request for a fixed principal, the
// agentclient.TokenSource a direct transport asks before each call.
type Tokens struct {
	Issuer  *Issuer
	Subject string
	TTL     time.Duration
}

// Token is a token for audience carrying scope.
func (t Tokens) Token(_ context.Context, audience, scope string) (string, error) {
	if t.Issuer == nil {
		return "", errors.New("token source has no issuer")
	}
	return t.Issuer.Mint(Claims{Audience: audience, Subject: t.Subject, Scopes: []Scope{Scope(scope)}}, t.TTL)
}
