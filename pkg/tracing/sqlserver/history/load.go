package history

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"time"

	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"

	"gorm.io/gorm"
)

// Loaded is what one read of the system_health files decoded: the graphs, and
// each report that would not decode as a row carrying its XML and the error.
// One report a decoder cannot read is that report's detail, not a reason to
// keep every other graph out of the store — the policy xesession.DecodeEvent
// applies to a deadlock report in a capture.
type Loaded struct {
	Graphs    []deadlockgraph.Graph
	Undecoded []Row
}

// Rows are the rows the read stores, oldest first.
func (l Loaded) Rows() ([]Row, error) {
	rows := make([]Row, 0, len(l.Graphs)+len(l.Undecoded))
	for _, graph := range l.Graphs {
		row, err := RowFromGraph(graph)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	rows = append(rows, l.Undecoded...)
	slices.SortFunc(rows, func(a, b Row) int {
		return cmp.Or(a.Timestamp.Compare(b.Timestamp), strings.Compare(a.ID, b.ID))
	})
	return rows, nil
}

// LoadWindow reads, decodes, resolves and analyses the deadlocks within
// options. It is the one loader behind Sync and a sql_deadlock span, so both
// see the same graphs.
func LoadWindow(ctx context.Context, db *gorm.DB, options ReadOptions) (Loaded, error) {
	source, err := ResolveSource(ctx, db)
	if err != nil {
		return Loaded{}, err
	}
	return loadFrom(ctx, db, source, options)
}

func loadFrom(ctx context.Context, db *gorm.DB, source Source, options ReadOptions) (Loaded, error) {
	events, err := Read(ctx, db, source, options)
	if err != nil {
		return Loaded{}, err
	}
	var loaded Loaded
	for _, event := range events {
		graph, decodeErr := deadlockgraph.DecodeEvent(event)
		if decodeErr == nil {
			loaded.Graphs = append(loaded.Graphs, graph)
			continue
		}
		row, err := UndecodedRow(event, decodeErr)
		if err != nil {
			return Loaded{}, err
		}
		loaded.Undecoded = append(loaded.Undecoded, row)
	}
	pool, err := db.DB()
	if err != nil {
		return Loaded{}, err
	}
	if err := deadlockgraph.Resolve(ctx, pool, loaded.Graphs); err != nil {
		return Loaded{}, err
	}
	for i := range loaded.Graphs {
		deadlockgraph.Analyze(&loaded.Graphs[i].Deadlock)
	}
	return loaded, nil
}

// UndecodedRow is the row a report the decoder refused is stored as: dated and
// keyed by its event, so a later sync skips it like any graph, and carrying the
// event XML and why it would not decode. It fails only when the event itself
// has no timestamp, since then it cannot be placed in the stream at all.
func UndecodedRow(eventXML string, cause error) (Row, error) {
	var event struct {
		Timestamp string `xml:"timestamp,attr"`
	}
	if err := xml.Unmarshal([]byte(eventXML), &event); err != nil {
		return Row{}, fmt.Errorf("deadlock report that would not decode (%v) is not an event either: %w", cause, err)
	}
	at, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return Row{}, fmt.Errorf("deadlock report that would not decode (%v) has no event timestamp: %w", cause, err)
	}
	sum := sha256.Sum256([]byte(eventXML))
	return Row{
		ID: deadlockgraph.FormatID(at, "undecoded-"+hex.EncodeToString(sum[:6])), Timestamp: at.UTC(),
		Databases: []string{}, Objects: []string{}, Indexes: []string{}, LockTypes: []string{}, Hosts: []string{}, Apps: []string{},
		Processes: []deadlockgraph.Process{}, Resources: []deadlockgraph.Resource{},
		DecodeError: cause.Error(), XML: eventXML,
	}, nil
}
