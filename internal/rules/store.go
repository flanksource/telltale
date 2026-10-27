package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	"os"
	"path/filepath"
	"time"
)

type Journal interface {
	Append(context.Context, Event) error
	Events(context.Context) ([]Event, error)
	Close() error
}
type journal struct{ backend recordstore.Backend }

func OpenJournal(workDir string) (Journal, error) {
	if err := os.MkdirAll(filepath.Join(workDir, ".telltale", "monitor"), 0700); err != nil {
		return nil, err
	}
	schemas := recordstore.NewSchemas()
	if err := schemas.Register("rule_event", []query.ColumnDef{{Name: "event", Type: query.ColumnTypeString}}, recordstore.KindOptions{}); err != nil {
		return nil, err
	}
	backend, err := sqlite.Open(sqlite.Options{Path: filepath.Join(workDir, ".telltale", "monitor", "events.sqlite"), Schema: schemas.Kind, SweepInterval: time.Hour})
	if err != nil {
		return nil, err
	}
	return &journal{backend: backend}, nil
}
func (j *journal) Append(ctx context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = j.backend.Append(ctx, "monitor", "rule_event", []recordstore.Row{{"event": string(data)}})
	return err
}
func (j *journal) Events(ctx context.Context) ([]Event, error) {
	events := []Event{}
	err := j.backend.Scan(ctx, "monitor", 0, func(_ int64, row recordstore.Row) error {
		data, ok := row["event"].(string)
		if !ok {
			return fmt.Errorf("rule event must be a JSON string")
		}
		var event Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	if errors.Is(err, recordstore.ErrNotFound) {
		return events, nil
	}
	return events, err
}
func (j *journal) Close() error { return j.backend.Close() }
