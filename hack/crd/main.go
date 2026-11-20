// Command crd writes the CustomResourceDefinitions reflected from
// pkg/definition (TraceDefinition) and pkg/deploy (Deployer) to crds/, run
// from the repository root by `task generate`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/deploy"
)

func main() {
	if err := write(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func write() error {
	for file, generate := range map[string]func() ([]byte, error){definition.CRDFile: definition.CRD, deploy.CRDFile: deploy.CRD} {
		data, err := generate()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
