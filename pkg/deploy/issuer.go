package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flanksource/commons-db/connection/store"
	"sigs.k8s.io/yaml"

	"github.com/flanksource/telltale/pkg/deploy/auth"
)

// DefaultIssuer is the issuer a Deployer built from flags signs with when
// none is named; EnsureIssuer creates it on first use.
const DefaultIssuer = "connection://telltale/issuer"

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// EnsureIssuer creates reference's connection, with a new signingKey, in the
// connections every Flanksource tool shares (<config>/flanksource/connections)
// unless the store already defines it, and reports whether it did. An
// existing issuer keeps its key, so agents provisioned with it stay valid.
func EnsureIssuer(connections *store.Store, reference string) (bool, error) {
	namespace, name, ok := strings.Cut(strings.TrimPrefix(reference, "connection://"), "/")
	if !strings.HasPrefix(reference, "connection://") || !ok || namespace == "" || name == "" || strings.Contains(name, "/") {
		return false, fmt.Errorf("issuer %q: want connection://<namespace>/<name>", reference)
	}
	existing, err := connections.Find(reference)
	if err != nil || existing != nil {
		return false, err
	}
	for _, layer := range connections.Layers() {
		if layer.Name == store.LayerGlobal {
			_, err := WriteIssuer(layer.Dir, namespace, name)
			return err == nil, err
		}
	}
	return false, fmt.Errorf("issuer %s: the connection store has no %s layer to create it in", reference, store.LayerGlobal)
}

// WriteIssuer writes dir/<name>.yaml: a connection whose signingKey secret is
// a new RSA key, for a Deployer's spec.auth.issuer. The key never leaves the
// file, which is private; agents get only its public JWKS. An existing file
// is never replaced.
func WriteIssuer(dir, namespace, name string) (string, error) {
	if !dnsLabel.MatchString(name) || (namespace != "" && !dnsLabel.MatchString(namespace)) {
		return "", fmt.Errorf("issuer connection %s/%s: namespace and name must be lowercase DNS labels", namespace, name)
	}
	key, err := auth.GenerateSigningKey()
	if err != nil {
		return "", err
	}
	metadata := map[string]string{"name": name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	data, err := yaml.Marshal(map[string]any{"apiVersion": "mission-control.flanksource.com/v1", "kind": "Connection", "metadata": metadata,
		// http with no url: an issuer is a JWKS publisher, served where spec.auth.jwksUrl says, if anywhere;
		// the connection store refuses a connection that declares no kind.
		"spec": map[string]any{"http": map[string]any{}, "secrets": map[string]any{SigningKeySecret: map[string]string{"value": string(key)}}}})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".yaml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write %s (an existing connection is never replaced): %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, file.Close()
}
