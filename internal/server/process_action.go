package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"syscall"

	"github.com/flanksource/telltale/internal/processcontrol"
)

const TopActionPath = TopMount + "/action"

func processAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(w, "cross-origin process actions are forbidden", http.StatusForbidden)
			return
		}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	var request processcontrol.Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one JSON request", http.StatusBadRequest)
		return
	}
	if err := processcontrol.Apply(r.Context(), request); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, processcontrol.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, processcontrol.ErrStale):
			status = http.StatusConflict
		case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
