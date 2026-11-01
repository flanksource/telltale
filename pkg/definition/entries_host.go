package definition

import (
	"errors"

	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/opensearch"
	"github.com/flanksource/telltale/pkg/tracing/prometheus"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlock"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/plancache"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xetrace"
	"github.com/flanksource/telltale/pkg/tracing/sqlserver/xevent"
)

// ProcessGroups records what each tool's processes on the host use.
type ProcessGroups struct {
	Named
	groups.Options
}

func (p *ProcessGroups) options(Located) (any, any, error) { return p.Options, nil, nil }

// Scrape scrapes Prometheus endpoints into the managed Prometheus.
type Scrape struct {
	Named
	prometheus.Options
}

func (s *Scrape) options(Located) (any, any, error) { return s.Options, nil, nil }

// OpenSearchSpans reads spans from an OpenSearch index.
type OpenSearchSpans struct {
	Named
	opensearch.Params
}

func (s *OpenSearchSpans) options(Located) (any, any, error) {
	params := s.Params
	return spans.Options{OpenSearch: &params}, nil, nil
}

func (s *OpenSearchSpans) connection(Located) connectionSlot {
	return connectionSlot{field: &s.Connection, connType: models.ConnectionTypeOpenTelemetry}
}

// XEvent captures a managed Extended Events session it creates on the server.
type XEvent struct {
	Named
	xevent.Options
}

func (x *XEvent) options(Located) (any, any, error) {
	if x.Session != "" {
		return nil, nil, errors.New("session attaches to an existing session: that is collect.sqlserver.systemHealth; xevent creates its own, named by sessionName")
	}
	return x.Options, nil, nil
}

func (x *XEvent) connection(Located) connectionSlot { return sqlServerSlot(&x.Connection) }

// SystemHealth attaches read-only to the server's system_health session.
type SystemHealth struct {
	Named
	xevent.Options
}

func (s *SystemHealth) options(Located) (any, any, error) {
	if s.SessionName != "" || s.MaxEvents != 0 || s.MaxMemoryKB != 0 {
		return nil, nil, errors.New("sessionName, maxEvents and maxMemoryKb configure a session telltale creates: that is instrument.sqlserver.xevent")
	}
	options := s.Options
	options.Session = xetrace.SystemHealthSession
	return options, nil, nil
}

func (s *SystemHealth) connection(Located) connectionSlot { return sqlServerSlot(&s.Connection) }

// PlanCache samples the plan cache's per-plan costs.
type PlanCache struct {
	Named
	plancache.Options
}

func (p *PlanCache) options(Located) (any, any, error) { return p.Options, nil, nil }

func (p *PlanCache) connection(Located) connectionSlot { return sqlServerSlot(&p.Connection) }

// DeadlockReport reads xml_deadlock_report events from system_health.
type DeadlockReport struct {
	Named
	deadlock.Options
}

func (d *DeadlockReport) options(Located) (any, any, error) { return d.Options, nil, nil }

func (d *DeadlockReport) connection(Located) connectionSlot { return sqlServerSlot(&d.Connection) }

func sqlServerSlot(field *string) connectionSlot {
	return connectionSlot{field: field, connType: models.ConnectionTypeSQLServer}
}

// KubernetesLogs follows the logs of a namespace's pods.
type KubernetesLogs struct {
	Named
	Namespace   string `json:"namespace" clicky:"required,title=Namespace"`
	PodSelector string `json:"podSelector" clicky:"required,title=Pod selector,desc=Label selector such as app=checkout"`
	Container   string `json:"container,omitempty" clicky:"title=Container"`
	Match       string `json:"match,omitempty" clicky:"title=Match"`
}

func (k *KubernetesLogs) options(Located) (any, any, error) {
	return logs.Options{Source: "kubernetes", Namespace: k.Namespace, PodSelector: k.PodSelector, Container: k.Container, Match: k.Match}, nil, nil
}

// ContainerLogs follows a local container's logs.
type ContainerLogs struct {
	Named
	Container string `json:"container" clicky:"required,title=Container"`
	Match     string `json:"match,omitempty" clicky:"title=Match"`
}

func (c *ContainerLogs) options(Located) (any, any, error) {
	return logs.Options{Source: "container", Container: c.Container, Match: c.Match}, nil, nil
}

// CloudLogs reads a log backend over a connection; its list fixes the backend.
type CloudLogs struct {
	Named
	Connection string         `json:"connection,omitempty" clicky:"title=Connection,desc=Empty takes the definition's default connection of the backend's type"`
	Query      string         `json:"query,omitempty" clicky:"title=Query"`
	MinLevel   string         `json:"minLevel,omitempty" clicky:"title=Minimum level"`
	Match      string         `json:"match,omitempty" clicky:"title=Match"`
	TimeField  string         `json:"timeField,omitempty" clicky:"title=Time field"`
	Options    map[string]any `json:"options,omitempty" clicky:"title=Options,desc=The backend's own options"`
}

func (c *CloudLogs) options(at Located) (any, any, error) {
	return logs.Options{
		Source: "cloud", Backend: at.Backend, Connection: c.Connection, Query: c.Query, MinLevel: c.MinLevel,
		Match: c.Match, TimeField: c.TimeField, ProviderOptions: c.Options,
	}, nil, nil
}

// logBackendConnections are the connection type each cloud logs backend reads.
var logBackendConnections = map[string]string{
	"loki": models.ConnectionTypeLoki, "cloudwatch": models.ConnectionTypeAWS, "opensearch": models.ConnectionTypeOpenSearch,
	"gcpcloudlogging": models.ConnectionTypeGCP, "azureloganalytics": models.ConnectionTypeAzure,
}

func (c *CloudLogs) connection(at Located) connectionSlot {
	return connectionSlot{field: &c.Connection, connType: logBackendConnections[at.Backend]}
}
