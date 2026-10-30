package server

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons-db/connection/store"
	"github.com/flanksource/telltale/pkg/datadir"
)

// storageDirs are the stores serve reads and writes, then the config dirs it
// reads in lookup order; Spans and RuleEvents are empty when --otlp or --rules
// left them closed.
type storageDirs struct {
	Runs        string
	Spans       string
	RuleEvents  string
	Config      []datadir.ConfigDir
	Connections []store.Layer
}

func (s storageDirs) String() string {
	var b strings.Builder
	b.WriteString("Telltale storage:\n")
	line := func(name, path string) {
		if path != "" {
			fmt.Fprintf(&b, "  %-22s%s\n", name, path)
		}
	}
	line("runs", s.Runs)
	line("OTLP spans", s.Spans)
	line("rule events", s.RuleEvents)
	for _, dir := range s.Config {
		line("config ("+dir.Name+")", dir.Dir)
	}
	for _, layer := range s.Connections {
		line("connections ("+layer.Name+")", layer.Dir)
	}
	return b.String()
}
