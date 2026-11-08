package records

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/flanksource/telltale/pkg/traces/read"
)

// FeedHandler serves the combined records of a single run, never arbitrary
// streams selected by the client.
func FeedHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selected, err := selectFeed(dataDir, r.PathValue("run"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() {
			if err := selected.Close(); err != nil {
				panic(http.ErrAbortHandler)
			}
		}()
		refs, err := filterFeed(selected.Refs, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		values := r.URL.Query()
		for key := range values {
			switch key {
			case "kind", "stream", "limit", "sort", "cursor", "follow", "format", "resume", "from", "until":
			default:
				http.Error(w, "unknown feed parameter: "+key, http.StatusBadRequest)
				return
			}
		}
		q := read.FeedQuery{Sort: values.Get("sort"), Query: read.Query{Cursor: values.Get("cursor")}}
		if q.Sort != "" && q.Sort != "seq" && q.Sort != "timestamp" {
			http.Error(w, "invalid record sort", http.StatusBadRequest)
			return
		}
		for name, destination := range map[string]*time.Time{"from": &q.From, "until": &q.Until} {
			if raw := values.Get(name); raw != "" {
				*destination, err = time.Parse(time.RFC3339Nano, raw)
				if err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
			}
		}
		if err := q.Validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if raw := values.Get("follow"); raw != "" {
			follow, parseErr := strconv.ParseBool(raw)
			if parseErr != nil {
				http.Error(w, parseErr.Error(), 400)
				return
			}
			if follow {
				if q.Cursor != "" || (values.Get("format") != "" && values.Get("format") != "ndjson") {
					http.Error(w, "invalid follow cursor or format", 400)
					return
				}
				streamFeed(w, r, refs, q.Query)
				return
			}
		}
		if raw := values.Get("limit"); raw != "" {
			q.Limit, err = strconv.Atoi(raw)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		page, err := read.FeedPage(r.Context(), refs, q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(page); err != nil {
			panic(http.ErrAbortHandler)
		}
	})
}

func selectFeed(dataDir, id string) (*read.Selection, error) {
	return read.ResolveRuns(read.Scope{DataDir: dataDir, Run: id})
}

func filterFeed(refs []read.Ref, r *http.Request) ([]read.Ref, error) {
	selected := []read.Ref{}
	for _, ref := range refs {
		owner, _ := ref.Runtime.Kinds.ResultOf(ref.Kind)
		if kind := r.URL.Query().Get("kind"); kind != "" && ref.Kind != kind && owner != kind {
			continue
		}
		if stream := r.URL.Query().Get("stream"); stream != "" && ref.Stream != stream {
			continue
		}
		selected = append(selected, ref)
	}
	if len(selected) == 0 && len(refs) != 0 {
		return nil, fmt.Errorf("capture selection is not armed by this run")
	}
	return selected, nil
}

type feedResume struct {
	From        time.Time         `json:"from"`
	Until       time.Time         `json:"until"`
	Run         string            `json:"run"`
	Positions   map[string]int64  `json:"positions"`
	Generations map[string]string `json:"generations"`
}

func streamFeed(w http.ResponseWriter, r *http.Request, refs []read.Ref, q read.Query) {
	if r.Header.Get("Last-Event-ID") == "" && r.URL.Query().Get("resume") != "" {
		r.Header.Set("Last-Event-ID", r.URL.Query().Get("resume"))
	}
	resume := feedResume{From: q.From, Until: q.Until, Run: r.PathValue("run"), Positions: map[string]int64{}, Generations: map[string]string{}}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		data, err := base64.RawURLEncoding.DecodeString(last)
		if err != nil || json.Unmarshal(data, &resume) != nil || resume.Run != r.PathValue("run") || len(resume.Positions) != len(refs) || !resume.From.Equal(q.From) || !resume.Until.Equal(q.Until) {
			http.Error(w, "invalid feed resume token", http.StatusBadRequest)
			return
		}
	}
	for i, ref := range refs {
		key := ref.Stream
		meta, err := ref.Runtime.Results.Backend.Meta(r.Context(), ref.Stream)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, hasPosition := resume.Positions[key]
		if last := r.Header.Get("Last-Event-ID"); last != "" && (!hasPosition || resume.Generations[key] != meta.Generation || resume.Positions[key] < 0 || resume.Positions[key] > meta.HighSeq) {
			http.Error(w, "feed resume stream changed", http.StatusBadRequest)
			return
		}
		refs[i].AfterSeq = max(ref.AfterSeq, resume.Positions[key])
		resume.Positions[key] = refs[i].AfterSeq
		resume.Generations[key] = meta.Generation
	}
	ndjson := r.URL.Query().Get("format") == "ndjson"
	w.Header().Set("Content-Type", "text/event-stream")
	if ndjson {
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	err := read.Feed(r.Context(), refs, q, func(record read.Record) error {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		resume.Positions[record.Stream] = record.Seq
		if ndjson {
			_, err = fmt.Fprintf(w, "%s\n", data)
		} else {
			token, marshalErr := json.Marshal(resume)
			if marshalErr != nil {
				return marshalErr
			}
			_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", base64.RawURLEncoding.EncodeToString(token), data)
		}
		if err != nil {
			return err
		}
		return controller.Flush()
	})
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		if ndjson {
			panic(http.ErrAbortHandler)
		}
		payload, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
		if marshalErr != nil {
			panic(http.ErrAbortHandler)
		}
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
	} else if !ndjson {
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	}
	_ = controller.Flush()
}
