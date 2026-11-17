package snapshot

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"time"
)

//go:embed telemetry.html
var telemetryPage string

var telemetryTemplate = template.Must(template.New("telemetry").Parse(telemetryPage))

type telemetrySnapshot struct {
	CapturedAt   time.Time              `json:"capturedAt"`
	Applications []telemetryApplication `json:"applications"`
}

type telemetryApplication struct {
	Loader string `json:"loader"`
	Label  string `json:"label"`
	Status struct {
		Armed           bool   `json:"armed"`
		Mode            string `json:"mode"`
		SeriesCount     int    `json:"seriesCount"`
		Errors          int64  `json:"errors"`
		AbandonedFrames int64  `json:"abandonedFrames"`
		Overflow        int64  `json:"overflow"`
		LastError       string `json:"lastError"`
	} `json:"status"`
	Histograms []telemetryHistogram `json:"histograms"`
}

type telemetryHistogram struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Count      int64             `json:"count"`
	SumSeconds float64           `json:"sumSeconds"`
	Buckets    map[string]int64  `json:"buckets"`
}

type telemetryBucket struct {
	Bound string
	Count int64
	limit float64
}

func (h telemetryHistogram) BucketRows() ([]telemetryBucket, error) {
	rows := make([]telemetryBucket, 0, len(h.Buckets))
	for bound, count := range h.Buckets {
		limit, err := strconv.ParseFloat(bound, 64)
		if err != nil || math.IsNaN(limit) || limit < 0 || math.IsInf(limit, 1) && bound != "+Inf" || count < 0 || count > h.Count {
			return nil, fmt.Errorf("invalid %s histogram bucket %q: %d", h.Name, bound, count)
		}
		rows = append(rows, telemetryBucket{Bound: bound, Count: count, limit: limit})
	}
	slices.SortFunc(rows, func(a, b telemetryBucket) int {
		if a.limit < b.limit {
			return -1
		}
		if a.limit > b.limit {
			return 1
		}
		return 0
	})
	for i := 1; i < len(rows); i++ {
		if rows[i].Count < rows[i-1].Count {
			return nil, fmt.Errorf("%s histogram buckets are not cumulative", h.Name)
		}
	}
	return rows, nil
}

func addTelemetryReport(result *Result) {
	data, ok := result.Files["telemetry.json"]
	var page []byte
	var err error
	if !ok {
		err = fmt.Errorf("telemetry.json is unavailable; see its collector outcome")
	} else {
		page, err = telemetryHTML(data)
	}
	result.add("telemetry.html", page, err)
}

func telemetryHTML(data []byte) ([]byte, error) {
	var snapshot telemetrySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode native telemetry: %w", err)
	}
	if snapshot.CapturedAt.IsZero() || snapshot.Applications == nil {
		return nil, fmt.Errorf("native telemetry requires capturedAt and an applications list")
	}
	for _, application := range snapshot.Applications {
		if application.Loader == "" || application.Status.Mode == "" || application.Histograms == nil {
			return nil, fmt.Errorf("native telemetry requires loader, status mode and histograms for every application")
		}
		for _, histogram := range application.Histograms {
			infinity, exists := histogram.Buckets["+Inf"]
			if histogram.Name == "" || histogram.Labels == nil || histogram.Count < 0 || histogram.SumSeconds < 0 || !exists || infinity != histogram.Count {
				return nil, fmt.Errorf("invalid %s native telemetry histogram", histogram.Name)
			}
		}
	}
	var escaped bytes.Buffer
	json.HTMLEscape(&escaped, data)
	var output bytes.Buffer
	err := telemetryTemplate.Execute(&output, struct {
		telemetrySnapshot
		JSON template.JS
	}{snapshot, template.JS(escaped.String())})
	return output.Bytes(), err
}
