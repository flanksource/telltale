package agentclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
)

// TokenStore keeps one file per minted token in the agent-tokens dir of
// each config dir.
type TokenStore struct{ dirs []datadir.ConfigDir }

// NewTokenStore keeps tokens under dirs: Lookup checks them in order, Mint
// writes to the global one.
func NewTokenStore(dirs []datadir.ConfigDir) *TokenStore {
	return &TokenStore{dirs: dirs}
}

const tokensDir = "agent-tokens"

func (s *TokenStore) Mint(name string, scope apitoken.Scope) (apitoken.Minted, error) {
	if err := apitoken.ValidateName(name); err != nil {
		return apitoken.Minted{}, err
	}
	if scope != apitoken.ScopeAgent && scope != apitoken.ScopeAPI {
		return apitoken.Minted{}, fmt.Errorf("invalid hub token scope %q: expected agent or api", scope)
	}
	minted, err := apitoken.Mint()
	if err != nil {
		return apitoken.Minted{}, err
	}
	record := apitoken.Record{ID: minted.ID, SecretHash: minted.Hash, Name: name, Scope: scope}
	data, err := json.Marshal(record)
	if err != nil {
		return apitoken.Minted{}, err
	}
	global, err := datadir.Global(s.dirs)
	if err != nil {
		return apitoken.Minted{}, fmt.Errorf("mint agent token: %w", err)
	}
	dir := filepath.Join(global, tokensDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return apitoken.Minted{}, fmt.Errorf("create agent token directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, minted.ID+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return apitoken.Minted{}, fmt.Errorf("create agent token: %w", err)
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		removeErr := os.Remove(filepath.Join(dir, minted.ID+".json"))
		return apitoken.Minted{}, fmt.Errorf("write agent token: %w", errors.Join(err, removeErr))
	}
	return minted, nil
}

func (s *TokenStore) Lookup(_ context.Context, id string) (apitoken.Record, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(decoded) != 9 || base64.RawURLEncoding.EncodeToString(decoded) != id {
		return apitoken.Record{}, apitoken.ErrUnknown
	}
	path, err := datadir.ConfigFile(s.dirs, filepath.Join(tokensDir, id+".json"))
	if err != nil {
		return apitoken.Record{}, fmt.Errorf("find agent token: %w", err)
	}
	if path == "" {
		return apitoken.Record{}, apitoken.ErrUnknown
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return apitoken.Record{}, fmt.Errorf("read agent token: %w", err)
	}
	var record apitoken.Record
	if err := json.Unmarshal(data, &record); err != nil {
		return apitoken.Record{}, fmt.Errorf("decode agent token: %w", err)
	}
	if record.ID != id || !record.Scope.Valid() || !apitoken.ValidHash(record.SecretHash) {
		return apitoken.Record{}, fmt.Errorf("invalid agent token record %q", id)
	}
	return record, nil
}
