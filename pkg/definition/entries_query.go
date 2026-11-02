package definition

import (
	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/query/providers"
	"github.com/flanksource/commons-db/query/providers/sqlite"
	tracequery "github.com/flanksource/commons-db/tracing/query"
)

// Window is when and how often a query entry reads, and how many rows one
// read keeps. It is declared after a provider's options in each entry, and
// shallower than them, so its limit is the one a provider's own shadows.
type Window struct {
	Params       map[string]any `json:"params,omitempty" clicky:"title=Params,desc=The query's params by name"`
	From         string         `json:"from,omitempty" clicky:"title=From,desc=Start of the window; a time or date math such as now-1h"`
	To           string         `json:"to,omitempty" clicky:"title=To,desc=End of the window; empty is now"`
	Every        string         `json:"every,omitempty" clicky:"title=Every,desc=Re-read the rows written since the last read at this interval such as 30s"`
	UntilStopped bool           `json:"untilStopped,omitempty" clicky:"title=Until stopped,desc=Read once when the run stops over the whole run"`
	Limit        int            `json:"limit,omitempty" clicky:"title=Limit,desc=Most rows one read keeps; 0 keeps all"`
}

func (w Window) params(provider *tracequery.Provider, profile string) tracequery.Params {
	return tracequery.Params{
		Profile: profile, Provider: provider, Params: w.Params, From: w.From, To: w.To,
		Every: w.Every, UntilStopped: w.UntilStopped, Limit: w.Limit,
	}
}

// Read is what every provider entry reads: a connection and a query.
type Read struct {
	Connection string `json:"connection,omitempty" clicky:"title=Connection,desc=Empty takes the definition's default connection of the provider's type"`
	Query      string `json:"query,omitempty" clicky:"title=Query"`
}

func (r Read) provider(at Located) *tracequery.Provider {
	return &tracequery.Provider{Type: at.Provider, Connection: r.Connection, Query: r.Query}
}

// SQLType is the SQL engine a sql query reads.
type SQLType string

// sqlTypes are the engines and the connection type each reads.
var sqlTypes = map[SQLType]string{
	"postgres": models.ConnectionTypePostgres, "mysql": models.ConnectionTypeMySQL, "sqlserver": models.ConnectionTypeSQLServer,
	"clickhouse": models.ConnectionTypeClickHouse, "sqlite": models.ConnectionTypeSQLite,
}

func (SQLType) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{"postgres", "mysql", "sqlserver", "clickhouse", "sqlite"}}
}

// SQLQuery reads a SQL statement with commons-db's generic SQL provider.
type SQLQuery struct {
	Named
	Read
	Type     SQLType `json:"type" clicky:"required,title=Type,desc=postgres mysql sqlserver clickhouse or sqlite"`
	URL      string  `json:"url,omitempty" clicky:"title=URL,desc=An inline DSN in place of a connection"`
	Database string  `json:"database,omitempty" clicky:"title=Database"`
	Window
}

func (q *SQLQuery) options(at Located) (any, any, error) {
	if err := oneOf("type", q.Type, SQLType("").JSONSchema()["enum"].([]any)); err != nil {
		return nil, nil, err
	}
	provider := q.provider(at)
	provider.SQL = &sqlite.SQLOptions{Type: sqlTypes[q.Type], URL: q.URL, Database: q.Database}
	return q.params(provider, ""), nil, nil
}

func (q *SQLQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: sqlTypes[q.Type], optional: q.URL != ""}
}

// PrometheusQuery reads PromQL, a range over the window when Step is set.
type PrometheusQuery struct {
	Named
	Read
	URL          string   `json:"url,omitempty" clicky:"title=URL,desc=A Prometheus URL in place of a connection"`
	Step         string   `json:"step,omitempty" clicky:"title=Step,desc=Range-query step such as 15s; empty reads instants"`
	SelectLabels []string `json:"selectLabels,omitempty" clicky:"title=Select labels"`
	Window
}

func (q *PrometheusQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.Prometheus = &providers.PrometheusOptions{URL: q.URL, SelectLabels: q.SelectLabels}
	if q.Step != "" {
		provider.Prometheus.Range = &providers.PrometheusRange{Step: q.Step}
	}
	return q.params(provider, ""), nil, nil
}

func (q *PrometheusQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypePrometheus, optional: q.URL != ""}
}

type openSearchOptions struct{ providers.OpenSearchOptions }

// OpenSearchQuery searches an OpenSearch index.
type OpenSearchQuery struct {
	Named
	Read
	openSearchOptions
	Window
}

func (q *OpenSearchQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.OpenSearch = &q.OpenSearchOptions
	return q.params(provider, ""), nil, nil
}

func (q *OpenSearchQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeOpenSearch, optional: q.Address != ""}
}

// HTTPQuery reads a JSON HTTP endpoint.
type HTTPQuery struct {
	Named
	Read
	providers.HTTPOptions
	Window
}

func (q *HTTPQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.HTTP = &q.HTTPOptions
	return q.params(provider, ""), nil, nil
}

func (q *HTTPQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeHTTP, optional: q.URL != ""}
}

// PostgRESTQuery reads a PostgREST endpoint.
type PostgRESTQuery struct {
	Named
	Read
	providers.PostgRESTOptions
	Window
}

func (q *PostgRESTQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.PostgREST = &q.PostgRESTOptions
	return q.params(provider, ""), nil, nil
}

func (q *PostgRESTQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeHTTP, optional: q.URL != ""}
}

// BigQueryQuery reads a BigQuery statement.
type BigQueryQuery struct {
	Named
	Read
	providers.BigQueryOptions
	Window
}

func (q *BigQueryQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.BigQuery = &q.BigQueryOptions
	return q.params(provider, ""), nil, nil
}

func (q *BigQueryQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeGCP}
}

type jaegerOptions struct{ providers.JaegerOptions }

// JaegerQuery reads traces from Jaeger.
type JaegerQuery struct {
	Named
	Read
	jaegerOptions
	Window
}

func (q *JaegerQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.Jaeger = &q.JaegerOptions
	return q.params(provider, ""), nil, nil
}

func (q *JaegerQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeJaeger, optional: q.URL != ""}
}

type openTelemetryOptions struct {
	providers.OpenTelemetryOptions
}

// OpenTelemetryQuery reads spans an OpenSearch index holds in OpenTelemetry form.
type OpenTelemetryQuery struct {
	Named
	Read
	openTelemetryOptions
	Window
}

func (q *OpenTelemetryQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.OpenTelemetry = &q.OpenTelemetryOptions
	return q.params(provider, ""), nil, nil
}

func (q *OpenTelemetryQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeOpenTelemetry}
}

// CloudWatchMetricsQuery reads CloudWatch metrics over the window, one point per Step.
type CloudWatchMetricsQuery struct {
	Named
	Read
	Language    string                       `json:"language,omitempty" clicky:"title=Language,desc=metrics (the listed metrics) metrics-insights (the query) or sql (metric SQL)"`
	Region      string                       `json:"region,omitempty" clicky:"title=Region"`
	Endpoint    string                       `json:"endpoint,omitempty" clicky:"title=Endpoint,desc=A CloudWatch endpoint in place of the region's"`
	Namespace   string                       `json:"namespace,omitempty" clicky:"title=Namespace,desc=Such as AWS/EC2"`
	Statistic   string                       `json:"statistic,omitempty" clicky:"title=Statistic,desc=Each metric's default; Average when empty"`
	Unit        string                       `json:"unit,omitempty" clicky:"title=Unit"`
	Step        string                       `json:"step" clicky:"required,title=Step,desc=The CloudWatch period such as 60s"`
	Metrics     []providers.CloudWatchMetric `json:"metrics,omitempty" clicky:"title=Metrics"`
	Dimensions  map[string]string            `json:"dimensions,omitempty" clicky:"title=Dimensions"`
	Concurrency int                          `json:"concurrency,omitempty" clicky:"title=Concurrency,desc=Parallel GetMetricData batches 1..32; 4 when empty"`
	Window
}

func (q *CloudWatchMetricsQuery) options(at Located) (any, any, error) {
	provider := q.provider(at)
	provider.CloudWatchMetrics = &providers.CloudWatchMetricsOptions{
		Language: q.Language, Region: q.Region, Endpoint: q.Endpoint, Namespace: q.Namespace, Statistic: q.Statistic, Unit: q.Unit,
		Range: &providers.PrometheusRange{Step: q.Step}, Metrics: q.Metrics, Dimensions: q.Dimensions, Concurrency: q.Concurrency,
	}
	return q.params(provider, ""), nil, nil
}

func (q *CloudWatchMetricsQuery) connection(Located) connectionSlot {
	return connectionSlot{field: &q.Connection, connType: models.ConnectionTypeAWS}
}

// ProfileQuery reads a saved commons-db query profile by name.
type ProfileQuery struct {
	Named
	Profile string `json:"profile" clicky:"required,title=Profile,desc=A saved profile of the config dirs' profile store"`
	Window
}

func (q *ProfileQuery) options(Located) (any, any, error) { return q.params(nil, q.Profile), nil, nil }
