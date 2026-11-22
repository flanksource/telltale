package records

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/entity"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/internal/livestream"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	"net/http"
	"strconv"
)

func ProcessGroupsHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.URL.Query() {
			if key != "stream" && key != "afterSeq" && key != "follow" {
				http.Error(w, "unknown process groups parameter: "+key, 400)
				return
			}
		}
		scope := read.Scope{DataDir: dataDir, Run: r.PathValue("run")}
		follow := r.URL.Query().Get("follow") == "1"
		var selected *read.Selection
		var err error
		if follow {
			selected, err = read.Resolve(processcapture.Kind, scope)
		} else {
			selected, err = selectRun(processcapture.Kind, scope)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer func() {
			if selected.Close() != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		refs, err := selectStreams(selected.Refs, r.URL.Query().Get("stream"))
		if err != nil || len(refs) != 1 {
			http.Error(w, fmt.Sprintf("select one process capture: %v", err), 400)
			return
		}
		after := int64(0)
		if value := r.URL.Query().Get("afterSeq"); value != "" {
			after, err = strconv.ParseInt(value, 10, 64)
		}
		if err != nil || after < 0 {
			http.Error(w, "invalid afterSeq", 400)
			return
		}
		ref := refs[0].TraceRef
		ref.AfterSeq = after
		if follow {
			streamGroups(w, r, refs[0], after)
			return
		}
		frames, err := processcapture.ReadFrames(r.Context(), refs[0].Runtime, ref)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewEncoder(w).Encode(frames) != nil {
			panic(http.ErrAbortHandler)
		}
	})
}

func streamGroups(w http.ResponseWriter, r *http.Request, ref read.Ref, after int64) {
	var generation string
	if cursor := r.Header.Get("Last-Event-ID"); cursor != "" {
		name, seq, ok := strings.Cut(cursor, ":")
		last, err := strconv.ParseInt(seq, 10, 64)
		if !ok || name == "" || err != nil || last < after {
			http.Error(w, "invalid process groups cursor", http.StatusBadRequest)
			return
		}
		generation, after = name, last
	}
	livestream.Serve(w, r, func(ctx context.Context, send entity.StreamSend) error {
		return livestream.Commits(ctx, ref.Runtime.Results.Backend, ref.Stream+"-metrics", func(meta recordstore.Meta) error {
			if generation != "" && generation != meta.Generation || after > meta.HighSeq || after > 0 && after < meta.LowSeq-1 {
				return fmt.Errorf("recorded process cursor expired; reload the recording")
			}
			if meta.HighSeq == after {
				return nil
			}
			window := ref.TraceRef
			window.AfterSeq = after
			frames, err := processcapture.ReadFrames(ctx, ref.Runtime, window)
			if err != nil {
				return err
			}
			after = frames.HighSeq
			return send(entity.StreamEvent{ID: meta.Generation + ":" + strconv.FormatInt(after, 10), Data: frames})
		})
	})
}
