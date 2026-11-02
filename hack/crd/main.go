// Command crd writes the TraceDefinition CustomResourceDefinition reflected
// from pkg/definition to definition.CRDFile, run from the repository root by
// `task generate`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/telltale/pkg/definition"
)

func main() {
	if err := write(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func write() error {
	data, err := definition.CRD()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(definition.CRDFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(definition.CRDFile, data, 0o644)
}
