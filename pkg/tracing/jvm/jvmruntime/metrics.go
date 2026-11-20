package jvmruntime

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/clicky/api"
)

// MetricsOp snapshots the JVM's memory pools, collectors, runtime and the
// Tomcat connector and data source pools it can find over JMX.
const MetricsOp = "jvm.metrics"

// Metrics is the agent's jvm.metrics snapshot. Connectors and DataSources
// carry whichever pool attributes the server exposes, so they stay maps.
type Metrics struct {
	CapturedAt        time.Time          `json:"capturedAt"`
	Memory            Memory             `json:"memory"`
	GarbageCollectors []GarbageCollector `json:"garbageCollectors"`
	Runtime           Runtime            `json:"runtime"`
	ProcessCPUPercent *float64           `json:"processCpuPercent,omitempty"`
	Connectors        []map[string]any   `json:"connectors"`
	DataSources       []map[string]any   `json:"dataSources"`
	Series            MetricSeries       `json:"series"`
}

// Memory is the JVM's pools by area.
type Memory struct {
	Heap    []MemoryPool `json:"heap"`
	NonHeap []MemoryPool `json:"nonHeap"`
	Buffer  []MemoryPool `json:"buffer"`
}

// MemoryPool is one pool's usage in bytes; Max is -1 when the pool is unbounded.
type MemoryPool struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Used      int64  `json:"used"`
	Committed int64  `json:"committed"`
	Max       int64  `json:"max"`
}

// GarbageCollector is one collector's lifetime totals; CollectionTime is millis.
type GarbageCollector struct {
	Name            string `json:"name"`
	CollectionCount int64  `json:"collectionCount"`
	CollectionTime  int64  `json:"collectionTime"`
}

// Runtime is the JVM and host it runs on; Uptime is millis and
// SystemLoadAverage is negative where the OS does not report one.
type Runtime struct {
	JavaVersion       string  `json:"javaVersion"`
	JavaHome          string  `json:"javaHome"`
	OSName            string  `json:"osName"`
	OSVersion         string  `json:"osVersion"`
	Processors        int     `json:"processors"`
	SystemLoadAverage float64 `json:"systemLoadAverage"`
	Uptime            int64   `json:"uptime"`
}

// MetricSeries names the collectors, connectors and data sources a
// timeseries sampler should track.
type MetricSeries struct {
	Collectors  []string `json:"collectors"`
	Connectors  []string `json:"connectors"`
	DataSources []string `json:"dataSources"`
}

// DecodeMetrics decodes the agent's snapshot, refusing one without the
// sections the agent always sends.
func DecodeMetrics(raw json.RawMessage) (Metrics, error) {
	metrics, err := decodeObject[Metrics](MetricsOp, raw, "capturedAt", "memory", "garbageCollectors", "runtime", "connectors", "dataSources", "series")
	if err == nil && metrics.Memory.Heap == nil {
		err = fmt.Errorf("%s returned no heap pools: %s", MetricsOp, raw)
	}
	return metrics, err
}

func (MemoryPool) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("name").Label("Pool").Build(),
		api.Column("type").Label("Area").Build(),
		api.Column("used").Label("Used").Build(),
		api.Column("committed").Label("Committed").Build(),
		api.Column("max").Label("Max").Build(),
	}
}

func (p MemoryPool) Row() map[string]any {
	limit := api.HumanizeBytes(p.Max)
	if p.Max < 0 {
		limit = api.Text{}.Append("unbounded", "text-muted")
	}
	return map[string]any{
		"name":      p.Name,
		"type":      strings.ReplaceAll(strings.ToLower(p.Type), "_", "-"),
		"used":      api.HumanizeBytes(p.Used),
		"committed": api.HumanizeBytes(p.Committed),
		"max":       limit,
	}
}

// Pretty is a runtime heading, the memory pool table, then one line for the
// collectors and one for each pool group the server exposes.
func (m Metrics) Pretty() api.Text {
	text := api.Text{}.Append("JVM metrics", "font-bold").Append(" "+m.runtimeSummary(), "text-muted").
		NewLine().Add(api.NewTableFrom(slices.Concat(m.Memory.Heap, m.Memory.NonHeap, m.Memory.Buffer)))
	var collectors []string
	for _, collector := range m.GarbageCollectors {
		collectors = append(collectors, fmt.Sprintf("%s %s in %s", collector.Name, api.HumanNumber(collector.CollectionCount).String(), millis(collector.CollectionTime)))
	}
	text = text.NewLine().Append("GC", "font-bold").Append(" " + strings.Join(collectors, " · "))
	for _, group := range []struct {
		label string
		pools []map[string]any
	}{{"Connectors", m.Connectors}, {"Data sources", m.DataSources}} {
		if len(group.pools) > 0 {
			text = text.NewLine().Append(group.label, "font-bold").Append(" " + poolSummary(group.pools))
		}
	}
	return text
}

func (m Metrics) runtimeSummary() string {
	facts := []string{"Java " + m.Runtime.JavaVersion, m.Runtime.OSName + " " + m.Runtime.OSVersion, fmt.Sprintf("%d cpus", m.Runtime.Processors)}
	if m.Runtime.SystemLoadAverage >= 0 {
		facts = append(facts, fmt.Sprintf("load %.2f", m.Runtime.SystemLoadAverage))
	}
	facts = append(facts, "up "+millis(m.Runtime.Uptime))
	if m.ProcessCPUPercent != nil {
		facts = append(facts, fmt.Sprintf("process cpu %.1f%%", *m.ProcessCPUPercent))
	}
	return strings.Join(append(facts, m.CapturedAt.UTC().Format(time.RFC3339)), " · ")
}

// poolSummary renders each pool as its name and attributes, objectName aside.
func poolSummary(pools []map[string]any) string {
	var out []string
	for _, pool := range pools {
		facts := []string{fmt.Sprint(pool["name"])}
		for _, key := range slices.Sorted(maps.Keys(pool)) {
			if key != "name" && key != "objectName" {
				facts = append(facts, fmt.Sprintf("%s=%v", key, pool[key]))
			}
		}
		out = append(out, strings.Join(facts, " "))
	}
	return strings.Join(out, " · ")
}
