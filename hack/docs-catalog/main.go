package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/flanksource/telltale/internal/cli"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/spf13/pflag"
)

type kind struct {
	tracing.KindInfo
	Flags []argument `json:"flags"`
}

type argument struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Required    bool   `json:"required"`
}

func main() {
	check := flag.Bool("check", false, "check that the documentation catalog is current")
	flag.Parse()
	kinds, err := traces.Kinds()
	if err != nil {
		fatal(err)
	}
	list, err := kinds.List()
	if err != nil {
		fatal(err)
	}
	root := cli.NewRoot("dev")
	entries := make([]kind, 0, len(list))
	for _, info := range list {
		cmd, _, err := root.Find([]string{"run", info.Name})
		if err != nil || cmd.Name() != info.Name {
			fatal(fmt.Errorf("find command for trace kind %s: %v", info.Name, err))
		}
		entry := kind{KindInfo: info, Flags: []argument{}}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden && f.Name != "help" {
				entry.Flags = append(entry.Flags, argument{Name: f.Name, Type: f.Value.Type(), Description: f.Usage, Default: f.DefValue, Required: len(f.Annotations["cobra_annotation_bash_completion_one_required_flag"]) > 0})
			}
		})
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fatal(err)
	}
	data = append(data, '\n')
	const path = "docs/_data/kinds.json"
	if *check {
		current, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		if !bytes.Equal(current, data) {
			fatal(fmt.Errorf("%s is stale; run go run ./hack/docs-catalog", path))
		}
		return
	}
	if err := os.MkdirAll("docs/_data", 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
