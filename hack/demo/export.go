package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traces/read"
)

type playgroundEvent struct {
	Variant    string    `json:"variant"`
	Seq        int64     `json:"seq"`
	ANSI       string    `json:"ansi"`
	DetailANSI string    `json:"detailAnsi"`
	Record     query.Row `json:"record"`
	Stream     string    `json:"stream"`
	Timestamp  any       `json:"timestamp"`
}

type playgroundKind struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Count       int               `json:"count"`
	Events      []playgroundEvent `json:"events"`
	SourceRunID string            `json:"sourceRunId"`
	rootPID     int
}

type playgroundRecording struct {
	RunID     string               `json:"runId"`
	StartedAt time.Time            `json:"startedAt"`
	EndedAt   time.Time            `json:"endedAt"`
	Kinds     []playgroundKind     `json:"kinds"`
	Processes read.ProcessSnapshot `json:"processes"`
}

func exportPlayground(ctx context.Context, o options) (err error) {
	info, err := telltale.Get(telltale.GetOptions{DataDir: o.dataDir, RunID: o.verifyRun})
	if err != nil {
		return err
	}
	if info.Status != telltale.StatusSucceeded {
		return fmt.Errorf("export needs a succeeded run, got %s", info.Status)
	}
	result := playgroundRecording{RunID: info.RunID, StartedAt: info.StartedAt, EndedAt: info.EndedAt}
	sources, closeSources, err := playgroundSources(ctx, o, info)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeSources()) }()
	process := sources["process"]
	refs, err := read.RunRefs(process.info, process.runtime)
	if err != nil {
		return err
	}
	result.Processes, err = read.Processes(ctx, refs)
	if err != nil {
		return err
	}
	for _, registration := range traces.Registrations() {
		source := sources["recording"]
		if registration.Name == "process" {
			source = sources["process"]
		}
		items, err := source.samples(ctx, registration)
		if err != nil {
			return err
		}
		result.Kinds = append(result.Kinds, items...)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.exportPlayground), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(o.exportPlayground, append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Exported %d recorded kinds to %s\n", len(result.Kinds), o.exportPlayground)
	return nil
}

func playgroundSamples(ctx context.Context, refs []read.Ref, item playgroundKind) (playgroundKind, error) {
	byVariant := map[string]read.Record{}
	q := read.FeedQuery{Query: read.Query{Limit: 500}}
	for {
		page, err := read.FeedPage(ctx, refs, q)
		if err != nil {
			return item, err
		}
		for _, record := range page.Rows {
			item.Count++
			if item.Name == "process" && number(record.Record["pid"]) != float64(item.rootPID) {
				continue
			}
			if item.Name == "process_metrics" && !strings.HasPrefix(text(record.Record["entity"]), fmt.Sprintf("%d@", item.rootPID)) {
				continue
			}
			if item.Name == "process_facets" && !strings.HasPrefix(text(record.Record["process"]), fmt.Sprintf("%d@", item.rootPID)) {
				continue
			}
			key := exportSampleKey(item.Name, record.Record)
			if key == "" {
				continue
			}
			previous, exists := byVariant[key]
			if !exists || number(record.Record["value"]) > number(previous.Record["value"]) {
				byVariant[key] = record
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(byVariant) == 0 {
		return item, fmt.Errorf("%s: no meaningful recorded playground sample", item.Name)
	}
	for variant, record := range byVariant {
		ansi, err := clicky.Format(record.Pretty(), clicky.FormatOptions{Format: "pretty"})
		if err != nil {
			return item, err
		}
		detail, err := clicky.Format(record.PrettyFull(), clicky.FormatOptions{Format: "pretty"})
		if err != nil {
			return item, err
		}
		item.Events = append(item.Events, playgroundEvent{Variant: variant, Seq: record.Seq, ANSI: ansi, DetailANSI: detail, Record: record.Record, Stream: record.Stream, Timestamp: record.Timestamp})
	}
	slices.SortFunc(item.Events, func(a, b playgroundEvent) int {
		if a.Seq < b.Seq {
			return -1
		}
		if a.Seq > b.Seq {
			return 1
		}
		return 0
	})
	return item, nil
}

func exportSampleKey(kind string, row query.Row) string {
	switch kind {
	case "exec":
		if filepath.Base(text(row["name"])) == "git" && meaningful(kind, row) {
			return "record"
		}
	case "terminal":
		return text(row["kind"])
	case "llm":
		return text(row["phase"])
	case "jvm_jfr":
		if meaningful(kind, row) {
			return text(row["event"])
		}
	case "sql_xevent":
		if row["name"] == "error_reported" {
			if number(row["errorNumber"]) == 1205 {
				return "error_reported"
			}
		} else if strings.Contains(text(row["sql"]), "orders") {
			return text(row["name"])
		}
	case "stacks":
		if meaningful(kind, row) {
			return text(row["state"])
		}
	case "process_metrics":
		if number(row["rssBytes"]) > 0 && row["cpuPercent"] != nil {
			return "record"
		}
	case "process_facets":
		if row["resource"] != nil {
			return text(row["facet"]) + "/" + text(row["operation"])
		}
	default:
		if meaningful(kind, row) {
			return "record"
		}
	}
	return ""
}
