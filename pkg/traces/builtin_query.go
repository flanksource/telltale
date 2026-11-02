package traces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/flanksource/commons-db/cmd/query/profiles"
	dbcontext "github.com/flanksource/commons-db/context"
	dbquery "github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/tracing/query"

	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

// QueryKind reads commons-db query profiles, saved or a provider in place.
const QueryKind = "query"

// ProfilesDir is where a config dir keeps its saved query profiles.
const ProfilesDir = "profiles"

// QueryParams are the query kind's params with the host target every
// telltale capture carries; the target is optional, as host is the only seam.
// Provider shadows the embedded one, the same type and JSON, so that a CLI
// takes it as one JSON flag rather than a flag per nested provider option.
type QueryParams struct {
	Target target.Target `json:"target,omitzero"`
	query.Params
	Provider *query.Provider `json:"provider,omitempty" clicky:"format=json,title=Provider,desc=A provider read in place: its type and connection or query and that provider's typed options"`
}

// Query is the params commons-db's handler reads.
func (p QueryParams) Query() query.Params {
	params := p.Params
	params.Provider = p.Provider
	return params
}

func (p QueryParams) Validate() error { return p.Query().Validate() }

// queryReads is commons-db's query handler under telltale's runtime: the two
// runtimes' emitters and record readers are the same interfaces.
type queryReads struct{ reads query.Reads }

func (q queryReads) Params() QueryParams {
	defaults := q.reads.Params()
	return QueryParams{Params: defaults, Provider: defaults.Provider}
}

func (q queryReads) Schema() recordresults.ResultType[query.Row] { return q.reads.Schema() }

func (q queryReads) Prepare(ctx dbcontext.Context, params QueryParams, out tracing.Emitter[query.Row]) (dbcontext.Context, func() error, error) {
	return q.reads.Prepare(ctx, params.Query(), out)
}

func (q queryReads) Handle(ctx dbcontext.Context, params QueryParams, out tracing.Emitter[query.Row], records tracing.Records[query.Row]) error {
	return q.reads.Handle(ctx, params.Query(), out, records)
}

// configProfiles resolves a saved profile from <config dir>/profiles, the
// first config dir that holds it winning, its imports resolved within it.
type configProfiles struct{}

func (configProfiles) Resolve(ctx context.Context, name string) (dbquery.Profile, error) {
	dirs, err := datadir.ConfigDirs()
	if err != nil {
		return dbquery.Profile{}, err
	}
	var searched []string
	for _, dir := range dirs {
		path := filepath.Join(dir.Dir, ProfilesDir)
		searched = append(searched, path)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		store, err := profiles.NewFileStore(path)
		if err != nil {
			return dbquery.Profile{}, err
		}
		saved, err := store.List(ctx)
		if err != nil {
			return dbquery.Profile{}, err
		}
		if !slices.ContainsFunc(saved, func(profile dbquery.Profile) bool { return profile.Name == name }) {
			continue
		}
		resolved, err := profiles.Resolve(ctx, store, name)
		return resolved.Profile, err
	}
	return dbquery.Profile{}, fmt.Errorf("no query profile %q in %v: %w", name, searched, os.ErrNotExist)
}

func init() {
	MustRegister(Registration{
		Name: QueryKind, Title: "Query", Seams: []target.Seam{target.Host}, DefaultSeam: target.Host,
		Summary: "Read a commons-db query profile, saved or a provider in place, over the run's window",
		Description: "Reads a saved profile from <config dir>/profiles, or a provider (sql, prometheus, opensearch, http, postgrest, bigquery, jaeger, opentelemetry) with its typed options, " +
			"once, once as the run stops (--until-stopped) or every --every interval appending the rows newer than the last read. " +
			"A windowed read binds the window onto the provider's range, else onto params.from and params.to, which its query must reference.",
		Examples: []Example{
			{Args: "--profile pod-cpu-by-pod --every 30s --duration 10m", Says: "poll a saved profile every 30 seconds for ten minutes"},
		},
		New: func(deps KindDeps) tracing.TracePlugin {
			reads, err := query.NewReads(query.Options{Profiles: configProfiles{}, BeforeExecute: deps.BeforeExecute})
			if err != nil {
				panic(fmt.Sprintf("query kind: %v", err))
			}
			capabilities := tracing.Capabilities{Live: query.Capabilities.Live, Historical: query.Capabilities.Historical, Follow: query.Capabilities.Follow}
			return tracing.NewHandler[QueryParams, query.Row](queryReads{reads}, capabilities, target.Host)
		},
	})
}
