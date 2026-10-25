package traceserve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/tracing"
)

// maxCheckpointBody bounds a checkpoint's params, which can carry a stack
// dumped elsewhere.
const maxCheckpointBody = 8 << 20

// Handler serves runtime's store, rooted at the path the host mounts it at
// (strip that prefix before it):
//
//	GET  /api/v1/traces/kinds              the kinds served, with their export formats
//	GET  /api/v1/traces/{kind}/seq         the seq a stream committed through (?stream)
//	GET  /api/v1/traces/{kind}/export      a window downloaded (?stream, ?afterSeq, ?toSeq, ?format)
//	POST /api/v1/sessions/{id}/checkpoint  a running capture's reading, committed (JSON body: the kind's checkpoint params)
//
// Every other path is 404.
func Handler(runtime *tracing.Runtime) (http.Handler, error) {
	if runtime == nil || runtime.Results == nil {
		return nil, errors.New("serve trace store: an open runtime is required")
	}
	h := handler{runtime: runtime}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+APIPrefix+"/traces/kinds", h.kinds)
	mux.HandleFunc("GET "+APIPrefix+"/traces/{kind}/seq", h.seq)
	mux.HandleFunc("GET "+APIPrefix+"/traces/{kind}/export", h.export)
	mux.HandleFunc("POST "+APIPrefix+"/sessions/{id}/checkpoint", h.checkpoint)
	return mux, nil
}

type handler struct {
	runtime *tracing.Runtime
}

func (h handler) kinds(w http.ResponseWriter, _ *http.Request) {
	kinds, err := h.runtime.Kinds.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, kinds)
}

// seq answers {"seq": n}, the seq ?stream of kind has committed through: 0 for
// one that never committed a row.
func (h handler) seq(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if _, ok := h.runtime.Kinds.Get(kind); !ok {
		http.Error(w, fmt.Sprintf("%v: %s", tracing.ErrUnknownKind, kind), http.StatusNotFound)
		return
	}
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		http.Error(w, "stream is required", http.StatusBadRequest)
		return
	}
	var seq int64
	meta, err := h.runtime.Results.Backend.Meta(r.Context(), stream)
	switch {
	case errors.Is(err, recordstore.ErrNotFound):
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	default:
		seq = meta.HighSeq
	}
	writeJSON(w, map[string]int64{"seq": seq})
}

// export downloads the window ?stream, ?afterSeq, ?toSeq of kind in ?format.
// A refusal found before the first byte is answered with its status; a failure
// once the download has begun aborts it, so a client never keeps a file cut
// short.
func (h handler) export(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	query := r.URL.Query()
	format, err := h.runtime.Kinds.Format(kind, query.Get("format"))
	if err != nil {
		http.Error(w, err.Error(), exportErrorStatus(err))
		return
	}
	ref, err := traceRef(kind, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	download := &attachment{w: w, format: format, filename: fmt.Sprintf("%s-%s.%s", kind, ref.Stream, format.Ext)}
	err = h.runtime.Export(r.Context(), ref, format.Name, download)
	switch {
	case err != nil && download.started:
		panic(http.ErrAbortHandler)
	case err != nil:
		http.Error(w, err.Error(), exportErrorStatus(err))
	default:
		// An empty export is still answered as a download; an empty write cannot fail.
		_, _ = download.Write(nil)
	}
}

// checkpoint has the running capture of session id take a reading, with the
// params its JSON body gives the kind, and commit what it emitted. A session
// that is no running capture of this runtime is a conflict; params its kind
// refuses or does not read are a bad request.
func (h handler) checkpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	params, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCheckpointBody))
	if err != nil {
		http.Error(w, fmt.Sprintf("checkpoint session %s: read params: %v", id, err), http.StatusBadRequest)
		return
	}
	if err := h.runtime.CheckpointSession(tracing.WithCheckpointParams(r.Context(), params), id); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, tracing.ErrNoCapture):
			status = http.StatusConflict
		case errors.Is(err, tracing.ErrCheckpointParams):
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// traceRef is the window query names of kind's stream.
func traceRef(kind string, query url.Values) (tracing.TraceRef, error) {
	ref := tracing.TraceRef{Kind: kind, Stream: query.Get("stream")}
	if ref.Stream == "" {
		return ref, errors.New("stream is required")
	}
	for name, seq := range map[string]*int64{"afterSeq": &ref.AfterSeq, "toSeq": &ref.ToSeq} {
		if raw := query.Get(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return ref, fmt.Errorf("%s %q is not a seq", name, raw)
			}
			*seq = parsed
		}
	}
	return ref, ref.Validate()
}

func exportErrorStatus(err error) int {
	switch {
	case errors.Is(err, tracing.ErrUnknownKind), errors.Is(err, recordstore.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, tracing.ErrUnknownFormat), errors.Is(err, tracing.ErrInvalidRef):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// attachment writes a download's headers with its first byte, so an export
// refused before it writes anything is still answered with its error.
type attachment struct {
	w        http.ResponseWriter
	format   tracing.ExportFormat
	filename string
	started  bool
}

// Write sets the download headers on the same writer it writes the body to, so
// the non-HTML content type is visibly bound to the untrusted bytes it carries.
func (a *attachment) Write(p []byte) (int, error) {
	w := a.w
	if !a.started {
		a.started = true
		header := w.Header()
		// A plugin's format must not turn captured bytes into an active browser document.
		switch a.format.MIME {
		case "application/x-ndjson", "application/json", "application/x-asciicast":
			header.Set("Content-Type", a.format.MIME)
		default:
			header.Set("Content-Type", "application/octet-stream")
		}
		header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.filename))
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", "sandbox")
		w.WriteHeader(http.StatusOK)
	}
	if len(p) == 0 {
		return 0, nil
	}
	return w.Write(p)
}
