// Package prometheus is a trace kind that scrapes Prometheus metrics endpoints
// into a Prometheus telltale manages: the capture hands its endpoints to that
// Prometheus as file_sd targets, labelled with the run and stream, and records
// one row per scrape Prometheus completes. The samples themselves stay in
// Prometheus's TSDB, queryable with PromQL after the capture ends.
package prometheus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// Kind is the name a host registers the kind as.
const Kind = "prometheus"

// How long a start waits for Prometheus to be ready and to discover the
// session's targets: file_sd changes reach the scrape manager at most every 5s.
const (
	readyWait     = 30 * time.Second
	discoveryWait = 20 * time.Second
)

// Scrape is the record the kind stores: one scrape of one endpoint.
type Scrape struct {
	TakenAt    time.Time `json:"takenAt" pretty:"label=Scraped" sort:"takenAt"`
	Endpoint   string    `json:"endpoint" filter:"terms"`
	Health     string    `json:"health" filter:"terms"`
	DurationMs float64   `json:"durationMs" pretty:"label=Duration,type=duration,unit=ms" sort:"durationMs"`
	Samples    int64     `json:"samples" sort:"samples"`
	Error      string    `json:"error"`
}

// Server is a Prometheus a capture's targets are handed to: its URL, the job
// whose file_sd files it reads, and their directory.
type Server struct {
	URL       string
	Job       string
	FileSDDir string
}

// KindOptions are what the kind's captures share: the run they record into, and
// the Prometheus they scrape with, started or found running on each start.
type KindOptions struct {
	RunID  string
	Server func(context.Context) (Server, error)
}

type kind struct{ options KindOptions }

func (kind) Params() Params { return Params{Options: Options{Interval: DefaultInterval.String()}} }

func (kind) Schema() recordresults.ResultType[Scrape] {
	return recordresults.ResultType[Scrape]{
		Title: "Prometheus scrapes", TimeColumn: "takenAt", Follow: true,
		Indexes:       []recordstore.IndexDef{{Columns: []string{"endpoint", "takenAt"}}},
		SearchColumns: []string{"endpoint", "error"},
	}
}

type clientKey struct{}

// Prepare hands the session's endpoints to Prometheus and waits until it
// scrapes them, refusing the start when Prometheus is not ready or never
// discovers them.
func (k kind) Prepare(ctx dbcontext.Context, params Params, out tracing.Emitter[Scrape]) (dbcontext.Context, func() error, error) {
	if k.options.RunID == "" || k.options.Server == nil {
		return ctx, nil, errors.New("a prometheus capture records into a telltale run, which gives it its run id and data dir")
	}
	stream := tracing.StreamOf(ctx)
	server, err := k.options.Server(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("start the managed prometheus: %w", err)
	}
	c := newClient(server)
	if err := c.ready(ctx, readyWait); err != nil {
		return ctx, nil, err
	}
	groups, err := targetGroups(params, session{run: k.options.RunID, stream: stream})
	if err != nil {
		return ctx, nil, err
	}
	path, err := writeTargets(server.FileSDDir, stream, groups)
	if err != nil {
		return ctx, nil, err
	}
	release := func() error { return os.Remove(path) }
	if err := awaitTargets(ctx, c, stream, len(params.Endpoints)); err != nil {
		return ctx, nil, errors.Join(err, release())
	}
	out.Describe(server.URL,
		query.SessionMetadata{Name: "job", Label: "Job", Value: server.Job},
		query.SessionMetadata{Name: "series", Label: "Series", Language: "promql", Value: fmt.Sprintf(`{%s=%q}`, LabelStream, stream)},
		query.SessionMetadata{Name: "targets", Label: "Target groups (" + path + ")", Language: "json", Value: string(groups)},
	)
	return ctx.WithValue(clientKey{}, c), release, nil
}

// awaitTargets waits until Prometheus lists the session's want targets.
func awaitTargets(ctx context.Context, c *client, stream string, want int) error {
	deadline := time.Now().Add(discoveryWait)
	for {
		targets, err := c.targets(ctx, stream)
		if err != nil {
			return err
		}
		if len(targets) >= want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prometheus %s lists %d of the session's %d targets in job %s after %s", c.server.URL, len(targets), want, c.server.Job, discoveryWait)
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

// Handle records each scrape Prometheus completes until the capture stops,
// polling twice an interval so no scrape goes unseen.
func (kind) Handle(ctx dbcontext.Context, params Params, out tracing.Emitter[Scrape], _ tracing.Records[Scrape]) error {
	interval, _, err := params.timing()
	if err != nil {
		return err
	}
	w := &watcher{client: ctx.Value(clientKey{}).(*client), stream: tracing.StreamOf(ctx), interval: interval, last: map[string]time.Time{}}
	ticker := time.NewTicker(interval / 2)
	defer ticker.Stop()
	for {
		scrapes, summary, err := w.poll(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		out.Report(summary)
		for _, scrape := range scrapes {
			if err := out.Emit(ctx, scrape); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// NewKind is the prometheus kind over options.
func NewKind(options KindOptions) *tracing.Handler[Params, Scrape] {
	return tracing.NewHandler[Params, Scrape](kind{options: options}, tracing.Capabilities{Live: true}, target.Host)
}
