package commonsdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/observability"
	"github.com/flanksource/commons/har"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
)

type HTTPEntry struct {
	har.Entry
	Feature string `json:"feature"`
}

type sqlProbe struct{}

func init() {
	telltale.RegisterHTTPObserver(armHTTP)
	telltale.RegisterProbe(sqlProbe{})
}

func armHTTP(ctx context.Context, raw json.RawMessage, sink telltale.Sink) (func() error, error) {
	var params struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("http params: %w", err)
	}
	for _, feature := range params.Features {
		if strings.TrimSpace(feature) == "" {
			return nil, fmt.Errorf("http feature must not be blank")
		}
	}
	var releases []func()
	for _, feature := range params.Features {
		config := dbcontext.NewContext(ctx).HARConfig(feature)
		config.CaptureSensitive = false
		collector := har.NewCollectorWithLifecycle(config, nil, func(entry *har.Entry) error {
			sink.TryEmit(HTTPEntry{Entry: *entry, Feature: feature})
			return nil
		})
		releases = append(releases, observability.ObserveHTTP(feature, collector))
	}
	return func() error {
		for _, release := range releases {
			release()
		}
		return nil
	}, nil
}

func (sqlProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "sql", Title: "SQL statements"}
}
func (sqlProbe) Arm(_ context.Context, raw json.RawMessage, sink telltale.Sink) (func() error, error) {
	var params struct {
		Connections []string `json:"connections"`
		MinDuration string   `json:"minDuration"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("sql params: %w", err)
	}
	if len(params.Connections) == 0 {
		return nil, fmt.Errorf("sql connections are required")
	}
	var minimum time.Duration
	if params.MinDuration != "" {
		value, err := time.ParseDuration(params.MinDuration)
		if err != nil || value < 0 {
			return nil, fmt.Errorf("invalid sql minDuration %q", params.MinDuration)
		}
		minimum = value
	}
	for _, name := range params.Connections {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("sql connection must not be blank")
		}
	}
	var releases []func()
	for _, name := range params.Connections {
		releases = append(releases, connection.ObserveSQL(name, func(statement connection.Statement) {
			if statement.Duration >= minimum {
				sink.TryEmit(statement)
			}
		}))
	}
	return func() error {
		for _, release := range releases {
			release()
		}
		return nil
	}, nil
}
