// Package prometheus configures VictoriaMetrics to scrape Prometheus exporters.
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

const discoveryWait = 20 * time.Second

// Scrape is the record format of historical scrape captures.
type Scrape struct {
	TakenAt    time.Time `json:"takenAt" pretty:"label=Scraped" sort:"takenAt"`
	Endpoint   string    `json:"endpoint" filter:"terms"`
	Health     string    `json:"health" filter:"terms"`
	DurationMs float64   `json:"durationMs" pretty:"label=Duration,type=duration,unit=ms" sort:"durationMs"`
	Samples    int64     `json:"samples" sort:"samples"`
	Error      string    `json:"error"`
}

// Server is where a capture hands off its targets: its URL, the job
// whose file_sd files it reads, and their directory.
type Server struct {
	URL       string
	Job       string
	FileSDDir string
}

// KindOptions are what the kind's captures share: the run they record into, and
// the VictoriaMetrics instance that scrapes them.
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

// Prepare hands the session's endpoints to VictoriaMetrics and waits for discovery.
func (k kind) Prepare(ctx dbcontext.Context, params Params, out tracing.Emitter[Scrape]) (_ dbcontext.Context, _ func() error, err error) {
	if k.options.RunID == "" || k.options.Server == nil {
		return ctx, nil, errors.New("a prometheus capture records into a telltale run, which gives it its run id and data dir")
	}
	stream := tracing.StreamOf(ctx)
	endpoints, closeEndpoints, err := resolveEndpoints(ctx, params.Endpoints)
	if err != nil {
		return ctx, nil, err
	}
	release := closeEndpoints
	defer func() {
		if err != nil {
			err = errors.Join(err, release())
		}
	}()
	server, err := k.options.Server(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("start managed VictoriaMetrics: %w", err)
	}
	c := newClient(server)
	groups, err := targetGroups(params, session{run: k.options.RunID, stream: stream}, endpoints)
	if err != nil {
		return ctx, nil, err
	}
	path, err := writeTargets(server.FileSDDir, stream, groups)
	if err != nil {
		return ctx, nil, err
	}
	release = func() error { return errors.Join(os.Remove(path), closeEndpoints()) }
	if err := awaitTargets(ctx, c, stream, len(endpoints)); err != nil {
		return ctx, nil, err
	}
	out.Describe(server.URL,
		query.SessionMetadata{Name: "job", Label: "Job", Value: server.Job},
		query.SessionMetadata{Name: "series", Label: "Series", Language: "promql", Value: fmt.Sprintf(`{%s=%q}`, LabelStream, stream)},
		query.SessionMetadata{Name: "targets", Label: "Target groups (" + path + ")", Language: "json", Value: string(groups)},
	)
	return ctx.WithValue(clientKey{}, c), release, nil
}

// awaitTargets waits until VictoriaMetrics lists all of the session's targets.
func awaitTargets(ctx context.Context, c *client, stream string, want int) error {
	ctx, cancel := context.WithTimeout(ctx, discoveryWait)
	defer cancel()
	for {
		targets, err := c.targets(ctx, stream)
		if err != nil {
			return err
		}
		if len(targets) >= want {
			return nil
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("VictoriaMetrics %s discovered %d of %d targets in %s: %w", c.server.URL, len(targets), want, c.server.Job, err)
		}
	}
}

// Handle reports target health while VictoriaMetrics stores the samples.
func (kind) Handle(ctx dbcontext.Context, params Params, out tracing.Emitter[Scrape], _ tracing.Records[Scrape]) error {
	interval, _, err := params.timing()
	if err != nil {
		return err
	}
	client := ctx.Value(clientKey{}).(*client)
	stream := tracing.StreamOf(ctx)
	ticker := time.NewTicker(min(interval/2, time.Second))
	defer ticker.Stop()
	for {
		targets, err := client.targets(ctx, stream)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		out.Report(targetHealth(client.server, targets))
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func targetHealth(server Server, targets []scrapeTarget) Summary {
	summary := Summary{Prometheus: server.URL, Job: server.Job, Targets: len(targets)}
	for _, target := range targets {
		if !target.LastScrape.After(time.Unix(0, 0)) {
			continue
		}
		switch target.Health {
		case "up":
			summary.Up++
		case "down":
			summary.Down++
			if summary.Errors == nil {
				summary.Errors = make(map[string]string)
			}
			summary.Errors[target.Labels[LabelEndpoint]] = target.LastError
		}
	}
	return summary
}

// Summary reports the current status of the capture's targets.
type Summary struct {
	Prometheus string            `json:"prometheus"`
	Job        string            `json:"job"`
	Targets    int               `json:"targets"`
	Up         int               `json:"up"`
	Down       int               `json:"down"`
	Errors     map[string]string `json:"errors,omitempty"`
}

// NewKind is the prometheus kind over options.
func NewKind(options KindOptions) *tracing.Handler[Params, Scrape] {
	return tracing.NewHandler[Params, Scrape](kind{options: options}, tracing.Capabilities{Live: true}, target.Host).WithTop(topSpec())
}
