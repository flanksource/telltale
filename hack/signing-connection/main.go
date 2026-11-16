// Command signing-connection writes a connection whose signingKey secret is a
// new RSA key, for a Deployer's spec.auth.issuer. The key never leaves the
// file: agents get only its public JWKS.
//
//	go run ./hack/signing-connection <name> <dir>
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"sigs.k8s.io/yaml"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: signing-connection <name> <dir>")
	}
	name, dir := args[0], args[1]
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(name) {
		return fmt.Errorf("connection name %q is not a lowercase DNS name", name)
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(map[string]any{
		"apiVersion": "mission-control.flanksource.com/v1",
		"kind":       "Connection",
		"metadata":   map[string]string{"name": name},
		"spec": map[string]any{
			"secrets": map[string]any{"signingKey": map[string]string{"value": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}},
		},
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, name+".yaml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s (an existing connection is never replaced): %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s: issuer connection://%s\n", path, name)
	return nil
}
