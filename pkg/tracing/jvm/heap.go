// Package jvm holds the typed results of Java agent diagnostic operations.
package jvm

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/clicky/api"
)

// HeapHistogramOp reads the heap's class histogram (GC.class_histogram). It
// changes nothing unless Live is set, which forces a full GC first.
const HeapHistogramOp = "jvm.heap.histogram"

// HeapHistogramParams are the agent's jvm.heap.histogram parameters.
type HeapHistogramParams struct {
	Top          int    `json:"top,omitempty"`
	ClassPattern string `json:"classPattern,omitempty"`
	Live         bool   `json:"live,omitempty"`
}

// Validate refuses what the agent would refuse; zero Top is the agent's default of 100.
func (p HeapHistogramParams) Validate() error {
	if p.Top < 0 {
		return fmt.Errorf("top must be a positive number of rows, got %d", p.Top)
	}
	return nil
}

// HeapHistogram is the heap's classes by retained bytes, largest first. The
// totals are the whole heap's; Matched is how many classes ClassPattern kept
// before the list was cut to Top.
type HeapHistogram struct {
	CollectedAt    time.Time   `json:"collectedAt"`
	Live           bool        `json:"live"`
	TotalInstances int64       `json:"totalInstances"`
	TotalBytes     int64       `json:"totalBytes"`
	Matched        int         `json:"matched"`
	Rows           []HeapClass `json:"rows"`
}

// HeapClass is one histogram row. Rank is the JDK's position in the full,
// unfiltered histogram; Loader is the module or loader the JDK printed beside
// the class, when it printed one.
type HeapClass struct {
	Rank      int    `json:"rank"`
	ClassName string `json:"className"`
	Instances int64  `json:"instances"`
	Bytes     int64  `json:"bytes"`
	Loader    string `json:"loader,omitempty"`
}

// DecodeHeapHistogram decodes the agent's result, refusing anything that is
// not a histogram rather than reading it as an empty heap.
func DecodeHeapHistogram(raw json.RawMessage) (HeapHistogram, error) {
	var histogram HeapHistogram
	if err := json.Unmarshal(raw, &histogram); err != nil {
		return histogram, fmt.Errorf("decode %s result: %w", HeapHistogramOp, err)
	}
	if histogram.CollectedAt.IsZero() || histogram.Rows == nil || histogram.TotalBytes <= 0 {
		return histogram, fmt.Errorf("%s returned no histogram: %s", HeapHistogramOp, raw)
	}
	return histogram, nil
}

func (HeapClass) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("rank").Label("#").Build(),
		api.Column("className").Label("Class").MaxWidth(100).Build(),
		api.Column("instances").Label("Instances").Build(),
		api.Column("bytes").Label("Bytes").Build(),
		api.Column("loader").Label("Loader").Build(),
	}
}

func (c HeapClass) Row() map[string]any {
	return map[string]any{
		"rank":      c.Rank,
		"className": c.ClassName,
		"instances": api.HumanNumber(c.Instances),
		"bytes":     api.HumanizeBytes(c.Bytes),
		"loader":    c.Loader,
	}
}

// Pretty is the histogram's heading over its HeapClass table.
func (h HeapHistogram) Pretty() api.Text {
	scope := "all objects"
	if h.Live {
		scope = "live objects"
	}
	return api.Text{}.Append("Heap histogram", "font-bold").
		Append(fmt.Sprintf(" %s · %s in %s instances · %d classes matched · %s", scope, api.HumanizeBytes(h.TotalBytes).String(), api.HumanNumber(h.TotalInstances).String(), h.Matched, h.CollectedAt.Format(time.RFC3339)), "text-muted").
		NewLine().Add(api.NewTableFrom(h.Rows))
}
