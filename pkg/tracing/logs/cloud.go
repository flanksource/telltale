package logs

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	dblogs "github.com/flanksource/commons-db/logs"
	"github.com/flanksource/commons-db/logs/azureloganalytics"
	"github.com/flanksource/commons-db/query"
	_ "github.com/flanksource/commons-db/query/providers"
)

func followCloud(ctx dbcontext.Context, p Params, emit func(Log) error) error {
	if p.Backend == "azureloganalytics" {
		resolved, err := ctx.HydrateConnectionByURL(p.Connection)
		if err != nil {
			return err
		}
		if resolved != nil && resolved.URL != "" {
			return azureCloud(ctx, p, resolved.URL, emit)
		}
	}
	provider, err := query.GetProvider(p.Backend)
	if err != nil {
		return err
	}
	options := maps.Clone(p.Options)
	if options == nil {
		options = make(map[string]any)
	}
	cutoff := time.Now().UTC().Format(time.RFC3339Nano)
	if p.Backend == "azureloganalytics" {
		if _, ok := options["start"]; !ok {
			options["start"] = "now-1h"
		}
	}
	if _, ok := options["end"]; !ok {
		options["end"] = cutoff
	}
	req := query.ProviderRequest{Provider: p.Backend, Connection: p.Connection, Query: p.Query, Options: options}
	if p.TimeField != "" {
		req.Order = query.Order{{Column: p.TimeField}, {Column: "_id", Unique: true}}
	}
	rows, err := provider.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("%s logs: %w", p.Backend, err)
	}
	for _, row := range rows {
		if err := emitCloud(row, p, emit); err != nil {
			return err
		}
	}
	if p.Backend == "opensearch" && len(rows) > 0 {
		var latest time.Time
		var latestID string
		for _, row := range rows {
			at, timeOK := row["timestamp"].(time.Time)
			id, idOK := row["id"].(string)
			if !timeOK || !idOK {
				return fmt.Errorf("opensearch history requires timestamp and id for follow")
			}
			if at.After(latest) || (at.Equal(latest) && id > latestID) {
				latest, latestID = at, id
			}
		}
		req.Position = query.CursorPosition{Keys: []any{latest, latestID}}
	}
	stream, ok := provider.(query.StreamProvider)
	if !ok {
		return nil
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamOptions := maps.Clone(options)
	delete(streamOptions, "end")
	streamOptions["start"] = cutoff
	req.Options = streamOptions
	var emitErr error
	err = stream.Stream(dbcontext.NewContext(streamCtx), req, func(row query.Row) {
		if emitErr == nil {
			emitErr = emitCloud(row, p, emit)
			if emitErr != nil {
				cancel()
			}
		}
	})
	if emitErr != nil {
		return emitErr
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func azureCloud(ctx dbcontext.Context, p Params, endpoint string, emit func(Log) error) error {
	options, err := query.DecodeOptions[struct {
		WorkspaceID string `json:"workspaceID"`
		Start       string `json:"start"`
		End         string `json:"end"`
		Limit       string `json:"limit"`
	}](p.Options)
	if err != nil {
		return err
	}
	config := cloud.AzurePublic
	config.Services = maps.Clone(config.Services)
	service := config.Services[azquery.ServiceNameLogs]
	service.Endpoint = endpoint
	config.Services[azquery.ServiceNameLogs] = service
	searcher := azureloganalytics.NewWithClientOptions(connection.AzureConnection{ConnectionName: p.Connection}, nil, &azquery.LogsClientOptions{ClientOptions: policy.ClientOptions{Cloud: config}})
	request := azureloganalytics.Request{WorkspaceID: options.WorkspaceID, Query: p.Query}
	request.Start, request.End, request.Limit = options.Start, options.End, options.Limit
	if request.Start == "" {
		request.Start = "now-1h"
	}
	if request.End == "" {
		request.End = time.Now().UTC().Format(time.RFC3339Nano)
	}
	result, err := searcher.Search(ctx, request)
	if err != nil {
		return fmt.Errorf("azureloganalytics logs: %w", err)
	}
	for _, line := range result.Logs {
		if line == nil {
			return fmt.Errorf("azureloganalytics returned a nil log")
		}
		row := query.Row{"message": line.Message, "timestamp": line.FirstObserved, "severity": line.Severity, "source": line.Source, "host": line.Host, "id": line.ID, "hash": line.Hash}
		for key, value := range line.Labels {
			row[key] = value
		}
		if err := emitCloud(row, p, emit); err != nil {
			return err
		}
	}
	return nil
}

func emitCloud(row query.Row, p Params, emit func(Log) error) error {
	message, ok := row["message"].(string)
	if !ok {
		return fmt.Errorf("cloud log message is %T, expected string", row["message"])
	}
	if !strings.Contains(message, p.Match) {
		return nil
	}
	if p.MinLevel != "" {
		var minimum, actual slog.Level
		if err := minimum.UnmarshalText([]byte(p.MinLevel)); err != nil {
			return fmt.Errorf("invalid minLevel %q: %w", p.MinLevel, err)
		}
		severity, ok := row["severity"].(string)
		if !ok {
			return fmt.Errorf("cloud log has no severity for minLevel")
		}
		normalized := strings.ToUpper(severity)
		switch normalized {
		case "WARNING":
			normalized = "WARN"
		case "NOTICE":
			normalized = "INFO"
		case "CRITICAL", "ALERT", "EMERGENCY", "FATAL":
			normalized = "ERROR"
		case "TRACE":
			normalized = "DEBUG-1"
		}
		if err := actual.UnmarshalText([]byte(normalized)); err != nil {
			return fmt.Errorf("cloud log severity %q: %w", severity, err)
		}
		if actual < minimum {
			return nil
		}
	}
	line := dblogs.LogLine{Message: message, Labels: make(map[string]string)}
	for key, value := range row {
		switch key {
		case "message":
		case "timestamp":
			if t, ok := value.(time.Time); ok {
				line.FirstObserved = t
			} else {
				return fmt.Errorf("cloud log timestamp is %T, expected time.Time", value)
			}
		case "severity":
			line.Severity = fmt.Sprint(value)
		case "source":
			line.Source = fmt.Sprint(value)
		case "host":
			line.Host = fmt.Sprint(value)
		case "id":
			line.ID = fmt.Sprint(value)
		case "hash":
			line.Hash = fmt.Sprint(value)
		default:
			line.Labels[key] = fmt.Sprint(value)
		}
	}
	resource := maps.Clone(line.Labels)
	switch p.Backend {
	case "cloudwatch":
		resource["cloud.provider"] = "aws"
	case "gcpcloudlogging":
		resource["cloud.provider"] = "gcp"
	case "azureloganalytics":
		resource["cloud.provider"] = "azure"
	}
	return emit(Log{LogLine: line, Resource: resource})
}
