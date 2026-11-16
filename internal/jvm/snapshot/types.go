package snapshot

import (
	"errors"
	"time"
)

type Options struct {
	Agent         string `json:"agent"`
	JFR           string `json:"jfr,omitempty"`
	HeapHistogram bool   `json:"heapHistogram,omitempty"`
}

func (o Options) Validate() error {
	if o.JFR != "" {
		duration, err := time.ParseDuration(o.JFR)
		if err != nil || duration <= 0 || duration > 5*time.Minute {
			return errors.New("jfr must be a positive duration at most 5m")
		}
	}
	return nil
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
