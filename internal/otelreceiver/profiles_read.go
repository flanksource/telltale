package otelreceiver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
)

type ProfileReadOptions struct {
	Kind, CaptureID, Source string
	Limit                   int
}

func (s *Store) ReadProfiles(ctx context.Context, options ProfileReadOptions) ([]profiles.Snapshot, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("OTLP store is closed")
	}
	if options.Kind != "" && options.Kind != "http_top" && options.Kind != "sql_top" {
		return nil, fmt.Errorf("unsupported profile kind %q", options.Kind)
	}
	if options.Limit < 0 {
		return nil, fmt.Errorf("profile read limit must be nonnegative")
	}
	if options.Limit == 0 {
		options.Limit = 10000
	}
	kinds := []string{options.Kind}
	if options.Kind == "" {
		kinds = []string{"http_top", "sql_top"}
	}
	result := []profiles.Snapshot{}
	for _, kind := range kinds {
		err := s.results.Backend.Scan(ctx, profileStream(kind)+"-frames", 0, func(_ int64, row recordstore.Row) error {
			encoded, err := json.Marshal(row)
			if err != nil {
				return err
			}
			var frame sampleframe.Frame
			if err = json.Unmarshal(encoded, &frame); err != nil {
				return err
			}
			native, ok := frame.Summary["otlp"].(string)
			if !ok {
				return fmt.Errorf("received profile frame has no native payload")
			}
			data, err := base64.StdEncoding.DecodeString(native)
			if err != nil {
				return err
			}
			snapshots, err := profiles.Decode(data, profiles.Options{})
			if err != nil {
				return err
			}
			for _, snapshot := range snapshots {
				if options.CaptureID != "" && snapshot.CaptureID != options.CaptureID || options.Source != "" && snapshot.Source != options.Source {
					continue
				}
				result = append(result, snapshot)
				if len(result) > options.Limit {
					return fmt.Errorf("profile read exceeds %d snapshots; narrow the selection", options.Limit)
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, recordstore.ErrNotFound) {
			return nil, err
		}
	}
	return result, nil
}
