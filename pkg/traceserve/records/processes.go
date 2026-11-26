package records

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/internal/livestream"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
)

func ProcessesHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		follow := params.Get("follow") == "1"
		stream := params.Get("stream")
		detail := follow && stream != "" && params.Get("view") == "detail" && len(params) == 3
		if r.URL.RawQuery != "" && !detail && !(follow && len(params) == 1) {
			http.Error(w, "process snapshot takes no query parameters", http.StatusBadRequest)
			return
		}
		selected, err := selectFeed(dataDir, r.PathValue("run"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() {
			if selected.Close() != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		if detail {
			for _, ref := range selected.Refs {
				if ref.Kind == processcapture.Kind && ref.Stream == stream {
					streamProcessDetails(w, r, ref)
					return
				}
			}
			http.Error(w, "unknown process capture", http.StatusBadRequest)
			return
		}
		if follow {
			streamProcesses(w, r, selected.Refs)
			return
		}
		snapshot, err := read.Processes(r.Context(), selected.Refs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewEncoder(w).Encode(snapshot) != nil {
			panic(http.ErrAbortHandler)
		}
	})
}

func streamProcessDetails(w http.ResponseWriter, r *http.Request, ref read.Ref) {
	livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
		return livestream.Commits(ctx, ref.Runtime.Results.Backend, ref.Stream+"-metrics", func(recordstore.Meta) error {
			details, err := processcapture.ReadDetails(ctx, ref.Runtime, ref.TraceRef, 2000)
			if err != nil {
				return err
			}
			return send(entity.StreamEvent{Data: details})
		})
	})
}

func streamProcesses(w http.ResponseWriter, r *http.Request, refs []read.Ref) {
	sources := []livestream.CommitSource{}
	for _, ref := range refs {
		if ref.Kind == processcapture.MetricsTable {
			sources = append(sources, func(ctx context.Context, notify func()) error {
				return livestream.Commits(ctx, ref.Runtime.Results.Backend, ref.Stream, func(recordstore.Meta) error { notify(); return nil })
			})
		}
	}
	livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
		return livestream.Watch(ctx, sources, func() error {
			snapshot, err := read.Processes(ctx, refs)
			if err != nil {
				return err
			}
			return send(entity.StreamEvent{Data: snapshot})
		})
	})
}
