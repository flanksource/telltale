package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
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
		return fmt.Errorf("prometheus %s%s: %w", c.server.URL, path, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("prometheus %s is unreachable: %w", c.server.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("prometheus %s%s: %w", c.server.URL, path, err)
	}
	var answer struct {
		Status    string          `json:"status"`
		ErrorType string          `json:"errorType"`
		Error     string          `json:"error"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Status != "success" {
		return fmt.Errorf("prometheus %s%s answered %s: %s %s %.200s", c.server.URL, path, response.Status, answer.ErrorType, answer.Error, body)
	}
	if err := json.Unmarshal(answer.Data, data); err != nil {
		return fmt.Errorf("prometheus %s%s data: %w", c.server.URL, path, err)
	}
	return nil
}

// ready waits up to wait for the server's /-/ready.
func (c *client) ready(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	var last string
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.server.URL, "/")+"/-/ready", nil)
		if err != nil {
			return err
		}
		response, err := c.http.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			last = response.Status
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prometheus %s is not ready after %s: %s", c.server.URL, wait, last)
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

// scrapeTarget is one target of /api/v1/targets.
type scrapeTarget struct {
	Labels             map[string]string `json:"labels"`
	Health             string            `json:"health"`
	LastError          string            `json:"lastError"`
	LastScrape         time.Time         `json:"lastScrape"`
	LastScrapeDuration float64           `json:"lastScrapeDuration"`
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

// point is one sample of a series.
type point struct {
	at    time.Time
	value float64
}

// series are the samples of a range query by each series' endpoint label.
func (c *client) series(ctx context.Context, promql string) (map[string][]point, error) {
	var data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	}
	if err := c.get(ctx, "/api/v1/query", url.Values{"query": {promql}}, &data); err != nil {
		return nil, err
	}
	if data.ResultType != "matrix" {
		return nil, fmt.Errorf("prometheus %s query %s: want a matrix, got %q", c.server.URL, promql, data.ResultType)
	}
	series := map[string][]point{}
	for _, result := range data.Result {
		endpoint := result.Metric[LabelEndpoint]
		for _, value := range result.Values {
			at, ok := value[0].(float64)
			text, isText := value[1].(string)
			parsed, err := strconv.ParseFloat(text, 64)
			if !ok || !isText || err != nil {
				return nil, fmt.Errorf("prometheus %s query %s: unreadable sample %v", c.server.URL, promql, value)
			}
			series[endpoint] = append(series[endpoint], point{at: time.UnixMilli(int64(math.Round(at * 1000))), value: parsed})
		}
	}
	return series, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
