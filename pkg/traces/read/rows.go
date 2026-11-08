package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/profilestore"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/cel-go/cel"
)

func (q Query) predicate(ctx context.Context) (*query.RowExpr, error) {
	if q.Where == "" {
		return nil, nil
	}
	template := gomplate.Template{Expression: q.Where}
	env, err := cel.NewEnv(gomplate.CompileEnvOptions(map[string]any{"row": map[string]any{}}, template)...)
	if err != nil {
		return nil, fmt.Errorf("CEL: %w", err)
	}
	ast, issues := env.Compile(q.Where)
	if issues.Err() != nil {
		return nil, fmt.Errorf("CEL: %w", issues.Err())
	}
	if ast.OutputType() != cel.BoolType && ast.OutputType() != cel.DynType && ast.OutputType() != cel.AnyType {
		return nil, fmt.Errorf("CEL predicate must return bool, got %s", ast.OutputType())
	}
	return query.CompileRowExpr(dbcontext.NewContext(ctx), q.Where)
}

func (r Ref) profile(ctx context.Context, name string) (dbcontext.Context, query.Profile, error) {
	if r.Runtime == nil {
		return dbcontext.Context{}, query.Profile{}, fmt.Errorf("read %s: missing runtime", r.Kind)
	}
	if err := r.Validate(); err != nil {
		return dbcontext.Context{}, query.Profile{}, err
	}
	meta, err := r.Runtime.Results.Backend.Meta(ctx, r.Stream)
	if err != nil {
		return dbcontext.Context{}, query.Profile{}, err
	}
	if meta.Kind != r.Kind {
		return dbcontext.Context{}, query.Profile{}, fmt.Errorf("stream %s holds %s, not %s", r.Stream, meta.Kind, r.Kind)
	}
	dbctx := dbcontext.NewContext(ctx).WithConnectionResolver(r.Runtime.Results.Registry.ResolveConnection)
	profile, err := r.Runtime.Results.Registry.Get(ctx, "traces/"+r.Kind+name)
	return dbctx, profile, err
}

func (q Query) params(ref Ref, p query.Profile) (map[string]any, error) {
	window := ref.TraceRef
	window.AfterSeq = max(window.AfterSeq, q.AfterSeq)
	if q.ToSeq > 0 && (window.ToSeq == 0 || q.ToSeq < window.ToSeq) {
		window.ToSeq = q.ToSeq
	}
	if err := window.Validate(); err != nil {
		return nil, err
	}
	params := window.Params()
	for name, value := range map[string]time.Time{"from": q.From, "to": q.Until} {
		if !value.IsZero() {
			params[name] = value.UTC().Format(time.RFC3339Nano)
		}
	}
	if q.Search != "" {
		params["q"] = q.Search
	}
	for _, selection := range q.Filters {
		name, value, _ := strings.Cut(selection, "=")
		key := "filter." + strings.TrimSpace(name)
		if previous, ok := params[key]; ok {
			params[key] = previous.(string) + "," + value
		} else {
			params[key] = value
		}
	}
	_, err := query.ResolveColumnFilters(p, params)
	return params, err
}

type position struct {
	Ref    int          `json:"ref"`
	Cursor query.Cursor `json:"cursor"`
	Scope  string       `json:"scope"`
}

func cursorScope(refs []Ref, q Query) (string, error) {
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		keys = append(keys, fmt.Sprintf("%s:%s:%s:%d:%d", ref.RunID, ref.Kind, ref.Stream, ref.AfterSeq, ref.ToSeq))
	}
	q.Cursor = ""
	q.NextCursor = nil
	q.Limit = 0
	q.All = false
	data, err := json.Marshal(struct {
		Refs  []string
		Query Query
	}{keys, q})
	if err != nil {
		return "", fmt.Errorf("encode cursor scope: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Rows walks profiles in selection order, preserving each profile's native order.
// Limit applies after CEL. NextCursor is delivered only at a resumable page boundary.
func Rows(ctx context.Context, refs []Ref, q Query) iter.Seq2[query.Row, error] {
	return func(yield func(query.Row, error) bool) {
		if err := q.Validate(); err != nil {
			yield(nil, err)
			return
		}
		predicate, err := q.predicate(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		scope, err := cursorScope(refs, q)
		if err != nil {
			yield(nil, err)
			return
		}
		pos := position{Scope: scope}
		if q.Cursor != "" {
			data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
			if err != nil {
				yield(nil, fmt.Errorf("invalid cursor: %w", err))
				return
			}
			var saved position
			if err = json.Unmarshal(data, &saved); err != nil || saved.Scope != pos.Scope || saved.Ref < 0 || saved.Ref >= len(refs) {
				yield(nil, fmt.Errorf("invalid cursor for this run selection and query"))
				return
			}
			pos = saved
		}
		remaining := q.Limit
		if remaining == 0 {
			remaining = 100
		}
		for index := pos.Ref; index < len(refs); index++ {
			ref := refs[index]
			dbctx, p, err := ref.profile(ctx, "")
			if err != nil {
				yield(nil, err)
				return
			}
			if q.seqOrder {
				p.Order = query.Order{{Column: "seq", Unique: true}}
			}
			params, err := q.params(ref, p)
			if err != nil {
				yield(nil, err)
				return
			}
			release, err := ref.Runtime.Results.Registry.BeforeExecute(ctx, []profilestore.ReadRequest{{Profile: p, Params: params}})
			if err != nil {
				yield(nil, err)
				return
			}
			stopped := func() bool {
				defer release()
				cursor := pos.Cursor
				for {
					page := query.PageRequest{Limit: 500, Strategy: query.PagingCursor, Cursor: cursor}
					if !q.All {
						page.Limit = min(remaining, 500)
					}
					var batch query.Page
					for read, err := range query.ExecutePages(dbctx, p, page, params) {
						if err != nil {
							yield(nil, err)
							return true
						}
						batch = read
						break
					}
					if batch.Truncated {
						yield(nil, fmt.Errorf("profile %s truncated the read", p.Name))
						return true
					}
					for _, row := range batch.Rows {
						row["runId"] = ref.RunID
						if predicate != nil {
							keep, err := predicate.Bool(map[string]any{"row": row})
							if err != nil {
								yield(nil, err)
								return true
							}
							if !keep {
								continue
							}
						}
						if !yield(row, nil) {
							return true
						}
						remaining--
					}
					if !q.All && remaining <= 0 {
						next := position{Ref: index, Cursor: batch.Next, Scope: pos.Scope}
						if !batch.HasMore {
							next.Ref++
							next.Cursor = ""
						}
						if next.Ref < len(refs) && q.NextCursor != nil {
							data, err := json.Marshal(next)
							if err != nil {
								yield(nil, err)
								return true
							}
							q.NextCursor(base64.RawURLEncoding.EncodeToString(data))
						}
						return true
					}
					if !batch.HasMore {
						return false
					}
					if batch.Next.IsZero() {
						yield(nil, fmt.Errorf("profile %s has more rows but no cursor", p.Name))
						return true
					}
					cursor = batch.Next
				}
			}()
			if stopped {
				return
			}
			pos.Cursor = ""
		}
	}
}

func View(ctx context.Context, ref Ref, name string) ([]query.Row, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, fmt.Errorf("invalid view name %q", name)
	}
	dbctx, p, err := ref.profile(ctx, "/"+name)
	if err != nil {
		return nil, err
	}
	params := ref.Params()
	release, err := ref.Runtime.Results.Registry.BeforeExecute(ctx, []profilestore.ReadRequest{{Profile: p, Params: params}})
	if err != nil {
		return nil, err
	}
	defer release()
	return query.CollectRows(query.Rows(query.ExecutePages(dbctx, p, query.PageRequest{Limit: 500, Strategy: query.PagingCursor}, params)))
}
