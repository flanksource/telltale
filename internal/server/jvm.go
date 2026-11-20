package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/flanksource/telltale/internal/jvm"
)

func jvmHandler(w http.ResponseWriter, r *http.Request) {
	request := jvm.Request{Agent: r.PathValue("agent")}
	if r.Method == http.MethodPost {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "expected one request object", http.StatusBadRequest)
			return
		}
		request.Agent = r.PathValue("agent")
	} else {
		request.Operation = "jvm.catalog"
	}
	value, err := jvm.Call(r.Context(), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(value); err != nil {
		panic(err)
	}
}
