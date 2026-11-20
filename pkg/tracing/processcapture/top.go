package processcapture

import (
	"context"
	"slices"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/profilestore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
)

func sampleProfile(ctx context.Context, runtime *tracing.Runtime, ref tracing.TraceRef, _ sampleframe.Frame) (query.Profile, error) {
	profile, err := runtime.Results.Registry.Get(ctx, "traces/"+MetricsTable)
	if err != nil {
		return query.Profile{}, err
	}
	identities, err := runtime.Results.Registry.Get(ctx, "traces/"+Kind)
	if err != nil {
		return query.Profile{}, err
	}
	stream := strings.TrimSuffix(ref.Stream, "-metrics")
	params := (tracing.TraceRef{Kind: Kind, Stream: stream}).Params()
	release, err := runtime.Results.Registry.BeforeExecute(ctx, []profilestore.ReadRequest{{Profile: identities, Params: params}})
	if err != nil {
		return query.Profile{}, err
	}
	defer release()
	identitySQL := strings.NewReplacer("{{.params.stream}}", "'"+strings.ReplaceAll(stream, "'", "''")+"'", "{{.params.afterSeq}}", "0", "{{.params.toSeq}}", "9223372036854775807", "{{.params.q}}", "''", "{{.params.rootsOnly}}", "0").Replace(identities.Query)
	profile.Query = `SELECT metrics.*, identities.pid, identities.name, identities.command, identities.parentKey, identities.groups FROM (` + profile.Query + `) metrics JOIN (` + identitySQL + `) identities ON identities.key = metrics.entity`
	columns := []query.ColumnDef{}
	for _, name := range []string{"pid", "name", "cpuPercent", "rssBytes", "command", "parentKey", "groups"} {
		for _, c := range append(slices.Clone(identities.Columns), profile.Columns...) {
			if c.Name == name {
				columns = append(columns, c)
				break
			}
		}
	}
	for _, c := range profile.Columns {
		if !slices.ContainsFunc(columns, func(existing query.ColumnDef) bool { return existing.Name == c.Name }) {
			columns = append(columns, c)
		}
	}
	profile.Columns, profile.Presenter = columns, nil
	profile.Query = "SELECT * FROM (" + profile.Query + ") WHERE {{.params.q}} = '' OR instr(lower(name || ' ' || command), lower({{.params.q}})) > 0"
	profile.Params = append(profile.Params, query.ParamDef{Name: "q", Label: "Search", Role: query.ParamRoleSearch, Default: ""})
	presented, err := query.ClickyColumns(profile)
	if err != nil {
		return query.Profile{}, err
	}
	for i := range presented {
		presented[i].DefaultHidden = i >= 5
	}
	profile.Presenter = samplePresenter{columns: presented}
	return profile, nil
}

type samplePresenter struct{ columns []api.ColumnDef }

func (p samplePresenter) Columns() []api.ColumnDef                    { return p.columns }
func (samplePresenter) Present(row query.Row) (map[string]any, error) { return row, nil }
func (samplePresenter) Detail(row query.Row) (api.Textable, error)    { return clicky.Map(row), nil }
