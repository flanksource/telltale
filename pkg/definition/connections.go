package definition

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flanksource/commons-db/types"
)

// referencePrefix starts a reference to a stored connection.
const referencePrefix = "connection://"

// ConnectionRef is one of a definition's default connections: a
// connection://<name> reference to the run's connection store, or an inline
// connection the run adds to it.
type ConnectionRef struct {
	Reference string
	Inline    *InlineConnection
}

// InlineConnection is a connection a definition carries, its credentials
// references a secret store resolves, never literal values.
type InlineConnection struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// URL may use the $(username) and $(password) placeholders commons-db
	// fills from the credentials, but carries no userinfo of its own.
	URL        string            `json:"url,omitempty"`
	Username   *Credential       `json:"username,omitempty"`
	Password   *Credential       `json:"password,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// Credential is a secret an inline connection reads: valueFrom alone, as a
// literal value would be stored in the definition.
type Credential struct {
	Value     string              `json:"value,omitempty"`
	ValueFrom *types.EnvVarSource `json:"valueFrom,omitempty"`
}

// EnvVar is the credential as commons-db resolves it.
func (c *Credential) EnvVar() types.EnvVar {
	if c == nil {
		return types.EnvVar{}
	}
	return types.EnvVar{ValueFrom: c.ValueFrom}
}

// JSONSchema is either form: the CRD keeps it free-form, Validate checks it.
func (ConnectionRef) JSONSchema() map[string]any {
	return map[string]any{
		"description":                          "connection://<name>, or an inline connection {name, type, url, username, password, properties}",
		"x-kubernetes-preserve-unknown-fields": true,
	}
}

func (c ConnectionRef) MarshalJSON() ([]byte, error) {
	if c.Inline != nil {
		return json.Marshal(c.Inline)
	}
	return json.Marshal(c.Reference)
}

func (c *ConnectionRef) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte(`"`)) {
		return json.Unmarshal(data, &c.Reference)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	c.Inline = &InlineConnection{}
	return decoder.Decode(c.Inline)
}

// Name is the name entries reference the connection by.
func (c ConnectionRef) Name() string {
	if c.Inline != nil {
		return c.Inline.Name
	}
	return strings.TrimPrefix(c.Reference, referencePrefix)
}

// validate refuses a reference that is not connection://, and an inline
// connection with no name or type or with a literal credential.
func (c ConnectionRef) validate() error {
	if c.Inline == nil {
		if !strings.HasPrefix(c.Reference, referencePrefix) || c.Name() == "" {
			return fmt.Errorf("%q is not a connection://<name> reference", c.Reference)
		}
		return nil
	}
	return c.Inline.validate()
}

func (c *InlineConnection) validate() error {
	var errs []error
	if c.Name == "" || c.Type == "" {
		errs = append(errs, errors.New("an inline connection needs a name and a type"))
	}
	for _, credential := range []struct {
		field string
		value *Credential
	}{{"username", c.Username}, {"password", c.Password}} {
		switch {
		case credential.value == nil:
		case credential.value.Value != "":
			errs = append(errs, fmt.Errorf("%s: a literal value would be stored in the definition; use valueFrom (onePassword resolves locally, secretKeyRef needs a cluster) or a connection:// reference", credential.field))
		case credential.value.ValueFrom == nil || credential.value.ValueFrom.IsEmpty():
			errs = append(errs, fmt.Errorf("%s: valueFrom names no secret", credential.field))
		}
	}
	return errors.Join(append(errs, literalUserinfo("url", c.URL))...)
}

// literalUserinfo refuses a URL whose userinfo is a literal: each part must
// be a placeholder, $(username) and $(password) that a connection fills from
// its credentials, or a $VAR a DSN is expanded with.
func literalUserinfo(field, raw string) error {
	_, rest, found := strings.Cut(raw, "://")
	if !found {
		return nil
	}
	authority, _, _ := strings.Cut(rest, "/")
	userinfo, _, hasUser := strings.Cut(authority, "@")
	if !hasUser {
		return nil
	}
	user, password, _ := strings.Cut(userinfo, ":")
	if !strings.HasPrefix(user, "$") || (password != "" && !strings.HasPrefix(password, "$")) {
		redacted := strings.Replace(raw, userinfo+"@", "***@", 1)
		return fmt.Errorf("%s: %s carries literal credentials; use $(username)/$(password) with valueFrom credentials, a $VAR, or a connection:// reference", field, redacted)
	}
	return nil
}
