package protocol

import (
	"fmt"
	"net/url"
	"path"

	"github.com/flanksource/commons/properties"
)

func HTTPBodyLimit(value string) (int64, error) {
	if value == "" {
		return 256 << 10, nil
	}
	limit, err := properties.ParseBytes(value)
	if err != nil || limit < 0 || limit > 4<<20 {
		return 0, fmt.Errorf("bodies %q must be between zero and 4MiB", value)
	}
	return limit, nil
}

type HTTPDirection string

const (
	HTTPIncoming HTTPDirection = "incoming"
	HTTPOutgoing HTTPDirection = "outgoing"
	HTTPBoth     HTTPDirection = "both"
)

func (HTTPDirection) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{string(HTTPIncoming), string(HTTPOutgoing), string(HTTPBoth)}, "default": string(HTTPBoth)}
}

type HTTPSelection struct {
	Direction HTTPDirection `json:"direction,omitempty" clicky:"title=Direction,desc=Incoming requests outgoing clients or both"`
	Hosts     []string      `json:"hosts,omitempty" clicky:"title=Hosts,desc=Host globs to record"`
	Paths     []string      `json:"paths,omitempty" clicky:"title=Paths,desc=URL path globs to record"`
}

func (s HTTPSelection) Validate() error {
	if s.Direction != "" && s.Direction != HTTPIncoming && s.Direction != HTTPOutgoing && s.Direction != HTTPBoth {
		return fmt.Errorf("direction %q must be incoming, outgoing or both", s.Direction)
	}
	for _, patterns := range []struct {
		name   string
		values []string
	}{{"hosts", s.Hosts}, {"paths", s.Paths}} {
		for _, pattern := range patterns.values {
			if _, err := path.Match(pattern, ""); err != nil || pattern == "" {
				return fmt.Errorf("%s contains an invalid glob %q", patterns.name, pattern)
			}
		}
	}
	return nil
}

func (s HTTPSelection) Matches(direction HTTPDirection, target *url.URL) bool {
	return (s.Direction == "" || s.Direction == HTTPBoth || s.Direction == direction) &&
		matchesHTTPGlob(s.Hosts, target.Hostname()) && matchesHTTPGlob(s.Paths, target.Path)
}

func matchesHTTPGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, value); matched {
			return true
		}
	}
	return len(patterns) == 0
}
