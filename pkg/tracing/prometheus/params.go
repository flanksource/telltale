package prometheus

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/target"
)

// DefaultInterval is how often an endpoint is scraped unless asked otherwise,
// Prometheus's own default.
const DefaultInterval = 15 * time.Second

// defaultTimeout is Prometheus's default scrape timeout; a shorter interval
// caps it.
const defaultTimeout = 10 * time.Second

// The labels telltale gives every target it scrapes, which params may not set.
const (
	LabelRun      = "telltale_run"
	LabelStream   = "telltale_stream"
	LabelEndpoint = "telltale_endpoint"
)

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Params choose the endpoints a capture scrapes, how often, and the labels
// their samples are stored with.
type Params struct {
	Endpoints []string          `json:"endpoints" clicky:"title=Endpoints,desc=Prometheus metrics URLs to scrape such as http://localhost:8080/metrics"`
	Interval  string            `json:"interval,omitempty" clicky:"title=Interval,desc=How often each endpoint is scraped such as 15s (at least 1s)"`
	Timeout   string            `json:"timeout,omitempty" clicky:"title=Timeout,desc=How long one scrape may take such as 5s (at most the interval; default the shorter of 10s and the interval)"`
	Labels    map[string]string `json:"labels,omitempty" clicky:"title=Labels,desc=Extra labels every scraped sample is stored with such as env=dev"`
	Target    target.Target     `json:"target,omitzero"`
}

func (p Params) Validate() error {
	var errs []error
	if len(p.Endpoints) == 0 {
		errs = append(errs, errors.New("at least one endpoint is required"))
	}
	seen := map[string]bool{}
	for _, raw := range p.Endpoints {
		if _, err := parseEndpoint(raw); err != nil {
			errs = append(errs, err)
		}
		if seen[raw] {
			errs = append(errs, fmt.Errorf("endpoint %q is given twice", raw))
		}
		seen[raw] = true
	}
	if _, _, err := p.timing(); err != nil {
		errs = append(errs, err)
	}
	for _, name := range slices.Sorted(maps.Keys(p.Labels)) {
		switch {
		case strings.HasPrefix(name, "__") || name == LabelRun || name == LabelStream || name == LabelEndpoint:
			errs = append(errs, fmt.Errorf("label %q is reserved: Prometheus owns __ labels and telltale sets %s, %s and %s", name, LabelRun, LabelStream, LabelEndpoint))
		case !labelName.MatchString(name):
			errs = append(errs, fmt.Errorf("label %q: a label name is letters, digits and underscores, not starting with a digit", name))
		}
	}
	return errors.Join(errs...)
}

// timing is the scrape interval and timeout the params ask for.
func (p Params) timing() (interval, timeout time.Duration, err error) {
	interval = DefaultInterval
	if p.Interval != "" {
		if interval, err = parseMillis("interval", p.Interval); err != nil {
			return 0, 0, err
		}
	}
	if interval < time.Second {
		return 0, 0, fmt.Errorf("interval %s is shorter than 1s", interval)
	}
	timeout = min(defaultTimeout, interval)
	if p.Timeout != "" {
		if timeout, err = parseMillis("timeout", p.Timeout); err != nil {
			return 0, 0, err
		}
	}
	if timeout <= 0 || timeout > interval {
		return 0, 0, fmt.Errorf("timeout %s must be positive and not longer than the interval %s", timeout, interval)
	}
	return interval, timeout, nil
}

// parseMillis parses a duration Prometheus can be given in milliseconds.
func parseMillis(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration such as 15s", name, value)
	}
	if duration%time.Millisecond != 0 {
		return 0, fmt.Errorf("%s %s must be whole milliseconds", name, duration)
	}
	return duration, nil
}

// promDuration is d as Prometheus reads a duration.
func promDuration(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

// endpoint is a metrics URL as a scrape target: its scheme, host:port, path
// and query params.
type endpoint struct {
	url, scheme, address, path string
	params                     map[string]string
}

func parseEndpoint(raw string) (endpoint, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return endpoint{}, fmt.Errorf("endpoint %q: want an absolute http(s) URL such as http://localhost:8080/metrics", raw)
	}
	if parsed.User != nil {
		return endpoint{}, fmt.Errorf("endpoint %q carries credentials, which a scrape target cannot", raw)
	}
	if parsed.Fragment != "" {
		return endpoint{}, fmt.Errorf("endpoint %q has a fragment, which a scrape never sends", raw)
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return endpoint{}, fmt.Errorf("endpoint %q query: %w", raw, err)
	}
	params := map[string]string{}
	for name, values := range query {
		if len(values) > 1 {
			return endpoint{}, fmt.Errorf("endpoint %q: query param %q is given more than once, which a scrape target cannot carry", raw, name)
		}
		params[name] = values[0]
	}
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[parsed.Scheme]
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return endpoint{url: raw, scheme: parsed.Scheme, address: net.JoinHostPort(parsed.Hostname(), port), path: path, params: params}, nil
}
