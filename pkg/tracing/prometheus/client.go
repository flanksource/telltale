package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/httpx"
)

// client reads the Prometheus API of one server.
type client struct {
	server Server
	http   *http.Client
}

func newClient(server Server) *client {
	return &client{server: server, http: httpx.Client(10 * time.Second)}
}

// get decodes the data of the API answer to path into data, refusing an
// unreachable server, an HTTP error and an API error alike.
func (c *client) get(ctx context.Context, path string, query url.Values, data any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.server.URL, "/")+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("VictoriaMetrics %s%s: %w", c.server.URL, path, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("VictoriaMetrics %s is unreachable: %w", c.server.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("VictoriaMetrics %s%s: %w", c.server.URL, path, err)
	}
	var answer struct {
		Status    string          `json:"status"`
		ErrorType string          `json:"errorType"`
		Error     string          `json:"error"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Status != "success" || response.StatusCode != http.StatusOK {
		return fmt.Errorf("VictoriaMetrics %s%s answered %s: %s %s %.200s", c.server.URL, path, response.Status, answer.ErrorType, answer.Error, body)
	}
	if err := json.Unmarshal(answer.Data, data); err != nil {
		return fmt.Errorf("VictoriaMetrics %s%s data: %w", c.server.URL, path, err)
	}
	return nil
}

func (c *client) health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.server.URL, "/")+"/health", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("VictoriaMetrics %s is unreachable: %w", c.server.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("VictoriaMetrics %s is not ready: %s", c.server.URL, response.Status)
	}
	return nil
}

// scrapeTarget is one target of /api/v1/targets.
type scrapeTarget struct {
	Labels     map[string]string `json:"labels"`
	Health     string            `json:"health"`
	LastError  string            `json:"lastError"`
	LastScrape time.Time         `json:"lastScrape"`
}

// targets are the active targets of the server's job labelled with stream.
func (c *client) targets(ctx context.Context, stream string) ([]scrapeTarget, error) {
	var data struct {
		ActiveTargets []scrapeTarget `json:"activeTargets"`
	}
	if err := c.get(ctx, "/api/v1/targets", url.Values{"state": {"active"}, "scrapePool": {c.server.Job}}, &data); err != nil {
		return nil, err
	}
	targets := make([]scrapeTarget, 0, len(data.ActiveTargets))
	for _, t := range data.ActiveTargets {
		if t.Labels[LabelStream] == stream {
			targets = append(targets, t)
		}
	}
	return targets, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
