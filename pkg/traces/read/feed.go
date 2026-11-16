package read

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/flanksource/telltale/pkg/tracing"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/profilestore"
	sqlpages "github.com/flanksource/commons-db/query/providers/sqlite"
)

type Record struct {
	presentation tracing.RecordPresentation
	Summary      string    `json:"summary"`
	RunID        string    `json:"runId"`
	Kind         string    `json:"kind"`
	Result       string    `json:"result,omitempty"`
	Stream       string    `json:"stream"`
	Seq          int64     `json:"seq"`
	Timestamp    any       `json:"timestamp"`
	Record       query.Row `json:"record"`
}

type FeedQuery struct {
	Query
	Sort string `json:"sort"`
}

type FeedResult struct {
	Rows       []Record `json:"rows"`
	NextCursor string   `json:"nextCursor"`
}

type feedWindow struct {
	Generation  string `json:"g"`
	High        int64  `json:"h"`
	Low         int64  `json:"l"`
	Compactions int64  `json:"c"`
}

type feedPosition struct {
	Scope   string            `json:"scope"`
	Windows []feedWindow      `json:"windows"`
	Keys    json.RawMessage   `json:"keys"`
	State   map[string][]byte `json:"state"`
}

func recordOf(ref Ref, row query.Row) (Record, error) {
	seq, ok := row["seq"].(int64)
	if !ok || seq < 1 {
		return Record{}, fmt.Errorf("%s/%s: invalid sequence %v", ref.Kind, ref.Stream, row["seq"])
	}
	plugin, ok := ref.Runtime.Kinds.Get(ref.Kind)
	if !ok {
		return Record{}, fmt.Errorf("unknown kind %s", ref.Kind)
	}
	schema, err := plugin.Schema(ref.Kind)
	if err != nil {
		return Record{}, err
	}
	presentation, err := ref.Runtime.Kinds.Present(ref.Kind, row)
	if err != nil {
		return Record{}, err
	}
	kind, result := ref.Runtime.Kinds.ResultOf(ref.Kind)
	return Record{presentation: presentation, Summary: presentation.Summary.String(), RunID: ref.RunID, Kind: kind, Result: result, Stream: ref.Stream, Seq: seq, Timestamp: row[schema.Options.TimeColumn], Record: maps.Clone(row)}, nil
}

// FeedPage reads one frozen window over every selected capture, through each
// kind's native profile and SQLite's common ordered-page merger.
func FeedPage(ctx context.Context, refs []Ref, q FeedQuery) (FeedResult, error) {
	result := FeedResult{Rows: []Record{}}
	if q.Sort == "" {
		q.Sort = "seq"
	}
	if q.Sort != "seq" && q.Sort != "timestamp" {
		return result, fmt.Errorf("unknown record sort %q", q.Sort)
	}
	if err := q.Validate(); err != nil {
		return result, err
	}
	if q.Where != "" || q.All {
		return result, fmt.Errorf("feed pages do not support CEL or unbounded reads")
	}
	if len(q.Cursor) > query.MaxCursorBytes {
		return result, fmt.Errorf("feed cursor exceeds maximum size")
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit > 500 {
		return result, fmt.Errorf("record page limit exceeds 500")
	}
	scope, err := cursorScope(refs, q.Query)
	if err != nil {
		return result, err
	}
	scope += ":" + q.Sort
	// Scope binds the exact ordered refs, so windows need no repeated stream names.
	saved := feedPosition{Scope: scope, Windows: make([]feedWindow, len(refs))}
	if q.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil {
			return result, fmt.Errorf("invalid feed cursor: %w", err)
		}
		if err := json.Unmarshal(data, &saved); err != nil {
			return result, err
		}
		if saved.Scope != scope || len(saved.Windows) != len(refs) {
			return result, fmt.Errorf("feed cursor no longer matches selection or sort")
		}
	}
	order := query.Order{{Column: "seq", Unique: true}}
	if q.Sort == "timestamp" {
		order = append(query.Order{{Column: "timestamp"}}, order...)
	}
	members := []sqlpages.SQLMember{}
	for index, ref := range refs {
		key := ref.RunID + "/" + ref.Kind + "/" + ref.Stream
		meta, err := ref.Runtime.Results.Backend.Meta(ctx, ref.Stream)
		if err != nil {
			return result, err
		}
		if q.Cursor == "" {
			saved.Windows[index] = feedWindow{Generation: meta.Generation, High: meta.HighSeq, Low: meta.LowSeq, Compactions: meta.Compactions}
		}
		window := saved.Windows[index]
		if window.Generation != meta.Generation {
			return result, fmt.Errorf("feed stream %s changed generation", key)
		}
		if window.High > 0 && (window.Low != meta.LowSeq || window.Compactions != meta.Compactions) {
			return result, fmt.Errorf("feed stream %s changed retained records", key)
		}
		if window.High == 0 {
			continue
		}
		ref.ToSeq = window.High
		member, err := feedMember(ctx, ref, q, order)
		if err != nil {
			return result, err
		}
		members = append(members, member)
	}
	if len(members) == 0 {
		return result, nil
	}
	position := query.CursorPosition{State: saved.State}
	if len(saved.Keys) > 0 {
		position.Keys, err = query.UnmarshalCursorKeys(saved.Keys)
		if err != nil {
			return result, err
		}
	}
	request := sqlpages.SQLMergeRequest{Members: members, Read: sqlpages.SQLPageRequest{Order: order, Position: position, Page: query.PageRequest{Limit: q.Limit, Strategy: query.PagingCursor, SkipTotal: true}}}
	for page, err := range sqlpages.MergeSQLPages(ctx, request) {
		if err != nil {
			return result, err
		}
		for _, row := range page.Rows {
			result.Rows = append(result.Rows, row["envelope"].(Record))
		}
		if page.HasMore {
			saved.Keys, err = query.MarshalCursorKeys(page.NextKeys)
			if err != nil {
				return result, err
			}
			saved.State = page.State
			encoded, err := json.Marshal(saved)
			if err != nil {
				return result, err
			}
			result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
			if len(result.NextCursor) > query.MaxCursorBytes {
				return result, fmt.Errorf("feed cursor exceeds maximum size")
			}
		}
		break
	}
	return result, nil
}

func feedMember(ctx context.Context, ref Ref, q FeedQuery, order query.Order) (sqlpages.SQLMember, error) {
	dbctx, profile, err := ref.profile(ctx, "")
	if err != nil {
		return sqlpages.SQLMember{}, err
	}
	plugin, _ := ref.Runtime.Kinds.Get(ref.Kind)
	schema, err := plugin.Schema(ref.Kind)
	if err != nil {
		return sqlpages.SQLMember{}, err
	}
	profile.Order = query.Order{{Column: "seq", Unique: true}}
	if q.Sort == "timestamp" {
		if schema.Options.TimeColumn == "" {
			return sqlpages.SQLMember{}, fmt.Errorf("kind %s has no timestamp column", ref.Kind)
		}
		profile.Order = append(query.Order{{Column: schema.Options.TimeColumn}}, profile.Order...)
	}
	params, err := q.params(ref, profile)
	if err != nil {
		return sqlpages.SQLMember{}, err
	}
	return sqlpages.SQLMember{Name: ref.RunID + "/" + ref.Kind + "/" + ref.Stream, ReadPage: func(ctx context.Context, request sqlpages.SQLPageRequest) (sqlpages.SQLPageResult, error) {
		release, err := ref.Runtime.Results.Registry.BeforeExecute(ctx, []profilestore.ReadRequest{{Profile: profile, Params: params}})
		if err != nil {
			return sqlpages.SQLPageResult{}, err
		}
		defer release()
		for page, err := range query.ExecutePages(dbctx.Wrap(ctx), profile, query.PageRequest{Limit: request.Page.Limit, Strategy: query.PagingCursor, After: request.Position.Keys}, params) {
			if err != nil {
				return sqlpages.SQLPageResult{}, err
			}
			rows := make([]query.Row, 0, len(page.Rows))
			for _, row := range page.Rows {
				record, err := recordOf(ref, row)
				if err != nil {
					return sqlpages.SQLPageResult{}, err
				}
				rows = append(rows, query.Row{"seq": record.Seq, "timestamp": record.Timestamp, "envelope": record})
			}
			return sqlpages.SQLPageResult{Rows: rows, HasMore: page.HasMore, Total: page.Total}, nil
		}
		return sqlpages.SQLPageResult{}, fmt.Errorf("profile %s returned no page", profile.Name)
	}}, nil
}
