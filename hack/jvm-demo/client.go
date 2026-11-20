package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiClient struct {
	opts options
	http *http.Client
}

type captureRequest struct{ kind, run string }

type jsonRequest struct {
	path   string
	output any
}

type recordsRequest struct {
	capture captureRequest
	output  any
}

func newClient(opts options) (apiClient, error) {
	if opts.artifacts == "" || opts.agent == "" {
		return apiClient{}, fmt.Errorf("artifacts directory and attached agent ID are required")
	}
	for _, endpoint := range []string{opts.server, opts.workload} {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil {
			return apiClient{}, fmt.Errorf("invalid loopback HTTP endpoint %q", endpoint)
		}
		if parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
			return apiClient{}, fmt.Errorf("demo endpoint must be loopback: %s", endpoint)
		}
	}
	switch opts.phase {
	case "active", "updated", "cleared":
		if opts.httpTop == "" || opts.sqlTop == "" {
			return apiClient{}, fmt.Errorf("capture run IDs for HTTP and SQL Top are required")
		}
	case "completed":
		if opts.http == "" || opts.sql == "" || opts.jms == "" || opts.spans == "" {
			return apiClient{}, fmt.Errorf("capture run IDs for HTTP, SQL, JMS and mirrored spans are required")
		}
	default:
		return apiClient{}, fmt.Errorf("unknown demo phase %q", opts.phase)
	}
	return apiClient{opts: opts, http: &http.Client{Timeout: 5 * time.Second}}, nil
}

func recordsPath(capture captureRequest) string {
	return "/api/traces/" + url.PathEscape(capture.run) + "/traces/" + url.PathEscape(capture.kind) + "/records"
}

func (c apiClient) get(ctx context.Context, input jsonRequest) (err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.opts.server, "/")+input.path, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("GET %s: %w", request.URL, err)
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return fmt.Errorf("read HTTP %d response: %w", response.StatusCode, err)
		}
		return fmt.Errorf("GET %s: HTTP %d: %s", request.URL, response.StatusCode, body)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(input.output); err != nil {
		return fmt.Errorf("decode GET %s: %w", request.URL, err)
	}
	return nil
}

func (c apiClient) records(ctx context.Context, input recordsRequest) error {
	var page struct {
		Rows       json.RawMessage `json:"rows"`
		NextCursor string          `json:"nextCursor"`
	}
	if err := c.get(ctx, jsonRequest{path: recordsPath(input.capture) + "?all=true", output: &page}); err != nil {
		return err
	}
	if page.NextCursor != "" {
		return fmt.Errorf("demo records unexpectedly paginated despite all=true")
	}
	if err := json.Unmarshal(page.Rows, input.output); err != nil {
		return fmt.Errorf("decode %s records: %w", input.capture.kind, err)
	}
	return nil
}

func (c apiClient) workloadState(ctx context.Context) error {
	var state struct {
		Ready   bool    `json:"ready"`
		Failure *string `json:"failure"`
	}
	workload := c
	workload.opts.server = c.opts.workload
	if err := workload.get(ctx, jsonRequest{path: "/control/state", output: &state}); err != nil {
		return err
	}
	if state.Failure != nil {
		return fmt.Errorf("demo workload failed: %s", *state.Failure)
	}
	if !state.Ready {
		return fmt.Errorf("demo workload is not ready")
	}
	return nil
}
