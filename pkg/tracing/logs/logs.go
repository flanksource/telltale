package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dbcontext "github.com/flanksource/commons-db/context"
	dblogs "github.com/flanksource/commons-db/logs"
	"github.com/flanksource/commons-db/logs/container"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

const Kind = "logs"

// Options choose the logs a capture records: those a process logs through the
// SDK, on the self or agent seam, or, with a Source, those a container,
// Kubernetes or a cloud backend serves, read on the host seam.
type Options struct {
	Levels      map[string]string `json:"levels,omitempty"`
	MinLevel    string            `json:"minLevel,omitempty"`
	Match       string            `json:"match,omitempty"`
	Source      string            `json:"source,omitempty"`
	Container   string            `json:"container,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	PodSelector string            `json:"podSelector,omitempty"`
	Backend     string            `json:"backend,omitempty"`
	Connection  string            `json:"connection,omitempty"`
	Query       string            `json:"query,omitempty"`
	// ProviderOptions are the cloud backend's own query options.
	ProviderOptions map[string]any `json:"options,omitempty"`
	TimeField       string         `json:"timeField,omitempty"`
}

// Params are a capture's Options on the seam it reads them through.
type Params struct {
	Target target.Target `json:"target,omitzero"`
	Options
}

// Validate refuses a source off the host seam, which alone reads one, and
// a host capture without one.
func (p Params) Validate() error {
	switch {
	case p.Target.Seam != target.Host && p.Source != "":
		return fmt.Errorf("%s logs are read on the %s seam, not %s", p.Source, target.Host, p.Target.Seam)
	case p.Target.Seam == target.Host && p.Source == "":
		return fmt.Errorf("the %s seam reads logs from a source: container, kubernetes or cloud", target.Host)
	}
	return p.Options.Validate()
}

// Validate is the static check of the options: it reaches no container,
// cluster or backend, and looks up no connection.
func (p Options) Validate() error {
	if p.Source == "" {
		return nil
	}
	if len(p.Levels) != 0 {
		return fmt.Errorf("logs levels require the agent seam")
	}
	switch p.Source {
	case "container":
		if p.Container == "" {
			return fmt.Errorf("container logs require a container name")
		}
	case "kubernetes":
		if p.Namespace == "" || p.PodSelector == "" {
			return fmt.Errorf("kubernetes logs require namespace and podSelector")
		}
	case "cloud":
		if p.Connection == "" || p.Backend == "" {
			return fmt.Errorf("cloud logs require backend and connection")
		}
		switch p.Backend {
		case "cloudwatch", "gcpcloudlogging", "azureloganalytics", "loki", "opensearch":
		default:
			return fmt.Errorf("unknown cloud logs backend %q", p.Backend)
		}
	default:
		return fmt.Errorf("unknown logs source %q: expected container, kubernetes or cloud", p.Source)
	}
	if p.MinLevel != "" && p.Source != "cloud" {
		return fmt.Errorf("%s logs do not expose severity for minLevel", p.Source)
	}
	return nil
}

type Log struct {
	dblogs.LogLine
	Logger   string            `json:"logger,omitempty"`
	TraceID  string            `json:"traceId,omitempty"`
	Resource map[string]string `json:"resource,omitempty"`
}

type capture struct{ agent seam.AgentSeam[Params, Log] }

func (c capture) AgentAvailable(probe string, t target.Target) (bool, error) {
	return c.agent.Available(probe, t)
}
func (capture) Params() Params { return Params{} }
func (capture) Schema() recordresults.ResultType[Log] {
	return recordresults.ResultType[Log]{Title: "Logs", TimeColumn: "firstObserved", Follow: true}
}
func (c capture) Prepare(ctx dbcontext.Context, p Params, out tracing.Emitter[Log]) (dbcontext.Context, func() error, error) {
	if p.Target.Seam == target.Host {
		if p.Source == "cloud" {
			if _, err := query.GetProvider(p.Backend); err != nil {
				return ctx, nil, err
			}
			connection, err := ctx.HydrateConnectionByURL(p.Connection)
			if err != nil {
				return ctx, nil, fmt.Errorf("resolve log connection %q: %w", p.Connection, err)
			}
			if connection == nil {
				return ctx, nil, fmt.Errorf("log connection %q not found", p.Connection)
			}
		}
		return ctx, nil, nil
	}
	levels, err := json.Marshal(p.Levels)
	if err != nil {
		return ctx, nil, err
	}
	prepared, release, err := c.agent.Prepare(ctx, p, out)
	if err != nil || len(p.Levels) == 0 {
		return prepared, release, err
	}
	out.Describe("log levels", query.SessionMetadata{Name: "levels", Label: "Temporary levels", Language: "json", Value: string(levels)})
	return prepared, release, nil
}
func (c capture) Handle(ctx dbcontext.Context, p Params, out tracing.Emitter[Log], _ tracing.Records[Log]) error {
	if p.Target.Seam == target.Host {
		if p.Source == "cloud" {
			return followCloud(ctx, p.Options, func(row Log) error { return out.Emit(ctx, row) })
		}
		if p.Source == "kubernetes" {
			client, err := ctx.LocalKubernetes()
			if err != nil {
				return fmt.Errorf("kubernetes client: %w", err)
			}
			return followKubernetes(ctx, client.Interface, p.Options, func(row Log) error { return out.Emit(ctx, row) })
		}
		followCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var emitErr error
		err := container.Follow(followCtx, container.FollowOptions{Container: p.Container, Follow: true, Emit: func(line dblogs.LogLine) {
			if emitErr == nil && strings.Contains(line.Message, p.Match) {
				emitErr = out.Emit(ctx, Log{LogLine: line, Resource: line.Labels})
				if emitErr != nil {
					cancel()
				}
			}
		}})
		if emitErr != nil {
			return emitErr
		}
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return c.agent.Handle(ctx, p, out)
}

func NewKind(registry *agentclient.Registry) *tracing.Handler[Params, Log] {
	agent := seam.AgentSeam[Params, Log]{Probe: Kind, Registry: registry, Target: func(p Params) target.Target { return p.Target }, Mutates: func(p Params) bool { return len(p.Levels) > 0 }, ArmParams: func(p Params) (json.RawMessage, error) { return json.Marshal(p) }}
	return tracing.NewHandler[Params, Log](capture{agent}, tracing.Capabilities{Live: true}, target.Self, target.Agent, target.Host).WithSecretMasking()
}
