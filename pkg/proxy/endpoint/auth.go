package endpoint

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/telltale/pkg/credentials/apitoken"
)

type Auth struct {
	Mode string `json:"mode,omitempty"`
}

func (a Auth) Validate() error {
	if a.Mode != "" && a.Mode != "passthrough" && a.Mode != "token" {
		return fmt.Errorf("auth.mode must be passthrough or token")
	}
	return nil
}

type Policy struct {
	Name       string `json:"name"`
	Connection string `json:"connection,omitempty"`
	Auth       Auth   `json:"auth"`
}

func (p Policy) Validate() error {
	if err := p.Auth.Validate(); err != nil {
		return err
	}
	if p.Name == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if p.Auth.Mode == "token" && (!strings.HasPrefix(p.Connection, "connection://") || strings.TrimPrefix(p.Connection, "connection://") == "") {
		return fmt.Errorf("token endpoint requires a connection:// backend")
	}
	return nil
}

func (p Policy) Authorize(ctx context.Context, raw string, verifier *apitoken.Verifier) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if p.Auth.Mode != "token" {
		return "", nil
	}
	if verifier == nil {
		return "", fmt.Errorf("token endpoint has no verifier")
	}
	record, err := verifier.VerifyScope(ctx, raw, apitoken.ScopeProxy)
	if err != nil {
		return "", err
	}
	if !slices.Contains(record.Endpoints, p.Name) {
		return "", apitoken.ErrScope
	}
	return record.ID, nil
}

func HTTPToken(header string) (string, error) {
	if value, ok := apitoken.BearerFromHeader(header); ok {
		return value, nil
	}
	scheme, value, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", apitoken.ErrMalformed
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", apitoken.ErrMalformed
	}
	user, password, ok := strings.Cut(string(decoded), ":")
	if !ok || user != "telltale" || password == "" {
		return "", apitoken.ErrMalformed
	}
	return password, nil
}
