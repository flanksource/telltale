package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/tracequery"
)

const otelPath = "/api/otel"

func otelHandler(service tracequery.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+otelPath+"/sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := service.Sources()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		connectionResponse(w, sources, nil, 200)
	})
	mux.HandleFunc("GET "+otelPath+"/form", func(w http.ResponseWriter, r *http.Request) {
		connectionResponse(w, map[string]any{"schema": rpc.SchemaForStruct(tracequery.Request{}), "defaults": tracequery.Request{Source: "records", Lookback: "1h", Limit: 100}}, nil, 200)
	})
	serve := func(w http.ResponseWriter, r *http.Request) {
		request, err := traceRequest(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if id := r.PathValue("id"); id != "" {
			detail, err := service.Trace(ctx, request, id)
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, tracequery.ErrTraceNotFound) {
					status = http.StatusNotFound
				}
				http.Error(w, err.Error(), status)
				return
			}
			connectionResponse(w, detail, nil, 200)
		} else {
			result, err := service.Search(ctx, request)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			connectionResponse(w, result, nil, 200)
		}
	}
	mux.HandleFunc("GET "+otelPath, serve)
	mux.HandleFunc("GET "+otelPath+"/trace/{id}", serve)
	return mux
}

func traceRequest(r *http.Request) (tracequery.Request, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return tracequery.Request{}, err
	}
	schema := rpc.SchemaForStruct(tracequery.Request{})
	body := map[string]any{}
	for key, values := range values {
		property, ok := schema.Properties[key]
		if !ok || len(values) != 1 {
			return tracequery.Request{}, fmt.Errorf("unknown or repeated trace parameter %q", key)
		}
		switch property.Type {
		case "integer":
			value, err := strconv.Atoi(values[0])
			if err != nil {
				return tracequery.Request{}, fmt.Errorf("%s must be an integer", key)
			}
			body[key] = value
		case "object":
			var value map[string]string
			if err := json.Unmarshal([]byte(values[0]), &value); err != nil {
				return tracequery.Request{}, fmt.Errorf("%s must be a JSON object of string values", key)
			}
			body[key] = value
		default:
			body[key] = values[0]
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return tracequery.Request{}, err
	}
	var request tracequery.Request
	if err := json.Unmarshal(data, &request); err != nil {
		return request, err
	}
	if request.Source == "" {
		return request, fmt.Errorf("source is required")
	}
	return request, nil
}
