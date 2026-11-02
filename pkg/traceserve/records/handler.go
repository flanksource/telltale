// Package records serves run records using the shared trace read API.
package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
)

// Handler serves GET records routes with run and kind path values. The host
// supplies the data directory; requests cannot choose another directory or run.
func Handler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, err := recordValues(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		scope, q, aggregate, err := read.ParseRequest(dataDir, values)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		scope.Run = r.PathValue("run")
		selected, err := selectRun(r.PathValue("kind"), scope)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		defer func() {
			if err := selected.Close(); err != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		refs, err := selectStreams(selected.Refs, values.Get("stream"))
		if err == nil && r.Header.Get("Accept") == "application/schema+json" {
			serveSchema(w, r, refs)
			return
		}
		if err == nil {
			err = read.ValidateSelection(r.Context(), refs, q)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if aggregate.Follow {
			if len(refs) != 1 {
				http.Error(w, "follow requires a single stream; specify stream", http.StatusBadRequest)
				return
			}
			follow(w, r, refs, q, values.Get("format"))
			return
		}
		servePage(w, r, refs, q, aggregate)
	})
}

func recordValues(r *http.Request) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	for name := range values {
		switch name {
		case "stream", "from", "until", "afterSeq", "toSeq", "filter", "where", "q", "limit", "cursor", "stats", "groupBy", "view", "follow", "format", "all":
		default:
			return nil, fmt.Errorf("unknown records parameter %q", name)
		}
	}
	if format := values.Get("format"); format != "" && format != "ndjson" {
		return nil, fmt.Errorf("unsupported records format %q", format)
	}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		seq, err := strconv.ParseInt(last, 10, 64)
		if err != nil || seq < 0 {
			return nil, fmt.Errorf("invalid Last-Event-ID %q", last)
		}
		after := int64(0)
		if raw := values.Get("afterSeq"); raw != "" {
			after, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || after < 0 {
				return nil, fmt.Errorf("invalid afterSeq %q", raw)
			}
		}
		values.Set("afterSeq", strconv.FormatInt(max(after, seq), 10))
	}
	return values, nil
}

func selectRun(kind string, scope read.Scope) (*read.Selection, error) {
	if scope.Run == "" || strings.ContainsAny(scope.Run, "/\\") {
		return nil, fmt.Errorf("invalid run %q", scope.Run)
	}
	run, live, err := telltale.Live(scope.DataDir, scope.Run)
	if err != nil {
		return nil, err
	}
	if !live {
		return read.Resolve(kind, scope)
	}
	runtime := run.Runtime()
	if _, ok := runtime.Kinds.Get(kind); !ok {
		return nil, fmt.Errorf("unknown trace kind %q", kind)
	}
	selected := &read.Selection{}
	for _, capture := range run.Info().Captures {
		if capture.Kind == kind {
			selected.Refs = append(selected.Refs, read.Ref{RunID: scope.Run, Runtime: runtime, TraceRef: capture.Ref})
		}
	}
	if len(selected.Refs) == 0 {
		return nil, fmt.Errorf("run %s has no %s capture", scope.Run, kind)
	}
	return selected, nil
}

func selectStreams(refs []read.Ref, stream string) ([]read.Ref, error) {
	if stream == "" {
		return refs, nil
	}
	var selected []read.Ref
	for _, ref := range refs {
		if ref.Stream == stream {
			selected = append(selected, ref)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("stream %q is not a capture of this run and kind", stream)
	}
	return selected, nil
}

type page struct {
	Rows       []query.Row `json:"rows"`
	NextCursor string      `json:"nextCursor"`
}

func servePage(w http.ResponseWriter, r *http.Request, refs []read.Ref, q read.Query, aggregate read.Aggregate) {
	result := page{Rows: []query.Row{}}
	q.NextCursor = func(cursor string) { result.NextCursor = cursor }
	var err error
	switch {
	case aggregate.View != "":
		result.Rows, err = viewRows(r.Context(), refs, q, aggregate.View)
	case aggregate.Enabled():
		result.Rows, err = read.Stats(read.Rows(r.Context(), refs, q), aggregate.Columns, aggregate.GroupBy)
	default:
		result.Rows, err = query.CollectRows(read.Rows(r.Context(), refs, q))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if result.Rows == nil {
		result.Rows = []query.Row{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func viewRows(ctx context.Context, refs []read.Ref, q read.Query, name string) ([]query.Row, error) {
	rows := []query.Row{}
	for _, ref := range refs {
		ref.AfterSeq = max(ref.AfterSeq, q.AfterSeq)
		if q.ToSeq > 0 && (ref.ToSeq == 0 || q.ToSeq < ref.ToSeq) {
			ref.ToSeq = q.ToSeq
		}
		part, err := read.View(ctx, ref, name)
		if err != nil {
			return nil, err
		}
		for _, row := range part {
			row["runId"] = ref.RunID
			rows = append(rows, row)
		}
	}
	return rows, nil
}
