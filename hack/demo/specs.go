package main

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
)

func demoSpecs(url, database string, javaPID int) ([]telltale.TraceSpec, error) {
	self := map[string]any{"seam": "self"}
	params := map[string]map[string]any{
		"process":       {"target": map[string]any{"seam": "process"}, "interval": "500ms", "facets": []string{"files", "sockets", "connections"}},
		"stacks":        {"target": self},
		"goprof":        {"target": self},
		"http":          {"target": self, "features": []string{"demo"}},
		"sql":           {"target": self, "connections": []string{"demo"}},
		"exec":          {"target": self, "tailBytes": 4096},
		"logs":          {"target": self, "minLevel": "info"},
		"spans":         {"target": self},
		"terminal":      {"target": self, "width": 100, "height": 24, "command": []string{"git", "--version"}},
		"jvm_jfr":       {"target": map[string]any{"seam": "process", "pid": javaPID}, "engine": "jfr", "settings": "profile", "threads": true},
		"prometheus":    {"target": map[string]any{"seam": "host"}, "endpoints": []string{url + "/metrics"}, "interval": "1s"},
		"query":         {"target": map[string]any{"seam": "host"}, "untilStopped": true, "provider": map[string]any{"type": "prometheus", "connection": "connection://telltale-prometheus", "query": "demo_requests_total", "prometheus": map[string]any{"range": map[string]any{"step": "1s"}}}},
		"sql_xevent":    {"connection": "connection://mssql", "databases": []string{database}, "sessionName": database, "poll": "200ms"},
		"sql_plancache": {"connection": "connection://mssql", "databases": []string{database}, "poll": "1s", "top": 20},
		"sql_deadlock":  {"connection": "connection://mssql", "databases": []string{database}, "poll": "1s"},
		"llm":           {"target": self, "includePrompts": true, "includeResponses": true},
	}
	if len(params) != len(traces.Registrations()) {
		return nil, fmt.Errorf("demo workload catalog has %d kinds; registry has %d", len(params), len(traces.Registrations()))
	}
	var specs []telltale.TraceSpec
	for _, registration := range traces.Registrations() {
		p, exists := params[registration.Name]
		if !exists {
			return nil, fmt.Errorf("no real demo workload for registered kind %s", registration.Name)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		spec := telltale.TraceSpec{Kind: registration.Name, Params: raw}
		if registration.Name == "goprof" {
			spec.Checkpoint = json.RawMessage(`{"profile":"heap","source":"gops","reason":"demo"}`)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}
