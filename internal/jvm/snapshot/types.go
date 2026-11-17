package snapshot

import (
	"errors"
	"fmt"
	"time"
)

type Options struct {
	Agent         string `json:"agent"`
	JFR           string `json:"jfr,omitempty"`
	HeapHistogram bool   `json:"heapHistogram,omitempty"`
	// Workload is how long to sample for workload.html; empty disables it.
	Workload string `json:"workload,omitempty"`
}

func (o Options) Validate() error {
	if o.JFR != "" {
		duration, err := time.ParseDuration(o.JFR)
		if err != nil || duration <= 0 || duration > 5*time.Minute {
			return errors.New("jfr must be a positive duration at most 5m")
		}
	}
	if o.Workload != "" {
		window, err := time.ParseDuration(o.Workload)
		if err != nil || window < workloadInterval || window > 5*time.Minute {
			return fmt.Errorf("workload must be a duration from %s (the sampling interval) to 5m", workloadInterval)
		}
	}
	return nil
}

// workloadSamples is how many samples span the Workload window, 0 when disabled.
func (o Options) workloadSamples() int {
	window, err := time.ParseDuration(o.Workload)
	if err != nil {
		return 0
	}
	return int(window/workloadInterval) + 1
}

type Outcome struct {
	File   string `json:"file"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type Manifest struct {
	Version     int       `json:"version"`
	Agent       string    `json:"agent"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	Complete    bool      `json:"complete"`
	Collectors  []Outcome `json:"collectors"`
}

type Result struct {
	Manifest Manifest
	Files    map[string][]byte
}
