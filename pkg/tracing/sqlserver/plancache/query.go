package plancache

import (
	"context"
	"fmt"

	"github.com/flanksource/clicky"
	dbquery "github.com/flanksource/commons-db/query"
	"gorm.io/gorm"
)

// DefaultLimit bounds plan-cache listings and filtered eviction selections.
const DefaultLimit = 100

// MaxLimit is the ceiling --limit may request.
const MaxLimit = 5000

// Filter is the selection surface shared by the listing and the evictor. One
// type, so the set an operator previewed is exactly the set that gets evicted.
type Filter struct {
	clicky.SortOptions

	Database clicky.MultiFilter `flag:"database" help:"Database name; supports * wildcards and ! exclusion"`
	Object   clicky.MultiFilter `flag:"object" help:"Procedure/trigger/view name; supports * wildcards and ! exclusion"`
	Text     clicky.MultiFilter `flag:"text" help:"Substring of the batch text; supports * wildcards and ! exclusion"`
	ObjType  clicky.MultiFilter `flag:"type" help:"Plan object type: Adhoc, Prepared, Proc, Trigger, View"`

	// PlanHandles is repeatable so one selection covers both "remove this plan"
	// and "remove the rows I ticked": a web table's checkbox selection is just a
	// handle list, and routing it through the same filter as everything else
	// keeps one execution path instead of a parallel id-mode.
	PlanHandles []string `flag:"plan-handle" help:"Exact plan handle(s) (0x…), as printed by the listing; repeatable"`
	QueryHash   string   `flag:"query-hash" help:"Exact query hash (0x…) — selects every plan compiled for the same statement shape"`

	Executions string `flag:"executions" help:"Execution count comparison (e.g. >5 or >=5,<=100); bare values mean >"`
	AvgCPU     string `flag:"avg-cpu" help:"Average CPU comparison (e.g. >500ms or >=1s,<2s); bare values mean >"`
	AvgElapsed string `flag:"avg-elapsed" help:"Average elapsed time comparison; bare values mean >"`
	TotalCPU   string `flag:"total-cpu" help:"Total CPU comparison; bare values mean >"`
	AvgReads   string `flag:"avg-reads" help:"Average logical reads comparison; bare values mean >"`
	UseCounts  string `flag:"use-counts" help:"Cache reuse count comparison; bare values mean >"`
	SizeKB     string `flag:"size-kb" help:"Cache size in KB comparison; bare values mean >"`
	UnusedFor  string `flag:"unused-for" help:"Time since last execution comparison (e.g. >24h); bare values mean >"`

	// IncludeStubs widens the listing past compiled plans to stubs, parse trees
	// and extended procs. Off by default: a stub carries no showplan and no
	// statistics, so it is noise in a diagnostic listing — but it does occupy
	// cache, which is why reclaiming space needs to see it.
	IncludeStubs bool `flag:"include-stubs" help:"Include Compiled Plan Stub / Parse Tree entries, not just compiled plans"`

	Limit  int `flag:"limit" help:"Limit number of results" default:"100"`
	Offset int `flag:"offset" help:"Offset for pagination" default:"0"`
}

// avgCPUMicros and avgReads are the sort/filter expressions for the two derived
// averages. NULLIF guards the divide: a cache entry with no query-stats row has
// execution_count NULL, and a stub has none at all.
const (
	avgCPUMicros     = "(qs.total_worker_time / NULLIF(qs.execution_count, 0))"
	avgElapsedMicros = "(qs.total_elapsed_time / NULLIF(qs.execution_count, 0))"
	avgReadsExpr     = "(qs.total_logical_reads / NULLIF(qs.execution_count, 0))"
)

// Sort whitelists the orderings the listing accepts. Keys are matched against
// the `SortKey` on CachedPlan.Columns(); SQLSortSpec.Validate pins the two
// together at registration time so a clickable header can never 400.
//
// The tie-breaker is the plan handle: it is the only unique column here, and
// without it OFFSET/FETCH paging can repeat or skip rows whose sort value ties.
var Sort = sortSpec{
	Default: clicky.SortOptions{Key: "avg-cpu", Direction: clicky.SortDirectionDesc},
	Columns: map[string]string{
		// Both are SQL-computed from the sql_text row, not filled in Go after the
		// fetch, so ordering by them is something the query can actually do.
		"database":       "DB_NAME(st.dbid)",
		"object":         "OBJECT_NAME(st.objectid, st.dbid)",
		"avg-cpu":        avgCPUMicros,
		"avg-elapsed":    avgElapsedMicros,
		"avg-reads":      avgReadsExpr,
		"total-cpu":      "qs.total_worker_time",
		"executions":     "qs.execution_count",
		"use-counts":     "cp.usecounts",
		"size":           "cp.size_in_bytes",
		"last-execution": "qs.last_execution_time",
		"type":           "cp.objtype",
	},
	TieBreakers: []string{"cp.plan_handle"},
}

// selectList is the projection. Column aliases are snake_case to match the
// `gorm:"column:…"` tags on cachedPlanRow.
const selectList = `
	cp.plan_handle                     AS plan_handle,
	cp.objtype                         AS obj_type,
	cp.cacheobjtype                    AS cache_obj_type,
	cp.usecounts                       AS use_counts,
	cp.size_in_bytes                   AS size_bytes,
	DB_NAME(st.dbid)                   AS database_name,
	OBJECT_NAME(st.objectid, st.dbid)  AS object_name,
	st.text                            AS sql_text,
	qs.execution_count                 AS execution_count,
	qs.total_worker_time               AS total_worker_time,
	qs.total_elapsed_time              AS total_elapsed_time,
	qs.total_logical_reads             AS total_logical_reads,
	qs.total_physical_reads            AS total_physical_reads,
	qs.total_writes                    AS total_writes,
	qs.total_rows                      AS total_rows,
	qs.creation_time                   AS creation_time,
	qs.last_execution_time             AS last_execution_time,
	qs.query_hash                      AS query_hash,
	qs.query_plan_hash                 AS query_plan_hash`

// statsAggregate rolls sys.dm_exec_query_stats (one row per STATEMENT) up to
// plan grain before it is joined.
//
// It is a derived table rather than correlated aggregates in the SELECT list
// because SQL Server rejects an aggregate over an expression containing a
// subquery ("Cannot perform an aggregate function on an expression containing an
// aggregate or a subquery"). GORM's DryRun renders either happily, so only a
// live query catches the difference — hence the shape is pinned by a test.
//
// LEFT JOIN, not INNER: a Compiled Plan Stub or Parse Tree has no query-stats
// row, and inner-joining would silently drop every entry that has never run.
const statsAggregate = `LEFT JOIN (
	SELECT plan_handle,
	       SUM(execution_count)      AS execution_count,
	       SUM(total_worker_time)    AS total_worker_time,
	       SUM(total_elapsed_time)   AS total_elapsed_time,
	       SUM(total_logical_reads)  AS total_logical_reads,
	       SUM(total_physical_reads) AS total_physical_reads,
	       SUM(total_logical_writes) AS total_writes,
	       SUM(total_rows)           AS total_rows,
	       MIN(creation_time)        AS creation_time,
	       MAX(last_execution_time)  AS last_execution_time,
	       MIN(query_hash)           AS query_hash,
	       MIN(query_plan_hash)      AS query_plan_hash
	FROM sys.dm_exec_query_stats
	GROUP BY plan_handle
) AS qs ON qs.plan_handle = cp.plan_handle`

// baseQuery builds the FROM/JOIN/WHERE half — the predicate the listing and the
// eviction selection share. It applies no ordering and no limit, so a caller
// that counts and a caller that pages see exactly the same set.
//
// The alias is `cp`, never `plan`/`pl`: PLAN is a reserved word in SQL Server and
// an unquoted `plan` alias corrupts OFFSET/FETCH paging.
func baseQuery(db *gorm.DB, f Filter) (*gorm.DB, error) {
	for _, patterns := range [][]string{f.Database, f.Object, f.Text, f.ObjType} {
		if err := validatePatterns(patterns); err != nil {
			return nil, err
		}
	}
	query := db.
		Table("sys.dm_exec_cached_plans AS cp").
		Joins("CROSS APPLY sys.dm_exec_sql_text(cp.plan_handle) AS st").
		Joins(statsAggregate).
		Where("CHARINDEX(?, st.text) = 0", pollMarker)

	if !f.IncludeStubs {
		query = query.Where("cp.cacheobjtype = ?", "Compiled Plan")
	}
	if len(f.Database) > 0 {
		query = whereMatchItems(query, "DB_NAME(st.dbid)", f.Database)
	} else {
		query = query.Where("st.dbid = DB_ID()")
	}
	if len(f.Object) > 0 {
		query = whereMatchItems(query, "OBJECT_NAME(st.objectid, st.dbid)", f.Object)
	}
	if len(f.Text) > 0 {
		// A bare token means "appears anywhere in the batch", which is what an
		// operator searching for a table name expects.
		query = whereMatchItems(query, "st.text", textFilterPatterns(f.Text))
	}
	if len(f.ObjType) > 0 {
		query = whereMatchItems(query, "cp.objtype", f.ObjType)
	}

	if len(f.PlanHandles) > 0 {
		handles := make([][]byte, 0, len(f.PlanHandles))
		for _, encoded := range f.PlanHandles {
			handle, err := DecodeHandle(encoded)
			if err != nil {
				return nil, fmt.Errorf("--plan-handle: %w", err)
			}
			handles = append(handles, handle)
		}
		query = query.Where("cp.plan_handle IN (?)", handles)
	}
	if f.QueryHash != "" {
		hash, err := DecodeHandle(f.QueryHash)
		if err != nil {
			return nil, fmt.Errorf("--query-hash: %w", err)
		}
		query = query.Where("qs.query_hash = ?", hash)
	}

	for _, selection := range []planRangeFilter{
		{value: f.Executions, expression: "qs.execution_count", kind: dbquery.ColumnFilterKindRange, scale: 1},
		{value: f.AvgCPU, expression: avgCPUMicros, kind: dbquery.ColumnFilterKindDuration, scale: 1000},
		{value: f.AvgElapsed, expression: avgElapsedMicros, kind: dbquery.ColumnFilterKindDuration, scale: 1000},
		{value: f.TotalCPU, expression: "qs.total_worker_time", kind: dbquery.ColumnFilterKindDuration, scale: 1000},
		{value: f.AvgReads, expression: avgReadsExpr, kind: dbquery.ColumnFilterKindRange, scale: 1},
		{value: f.UseCounts, expression: "cp.usecounts", kind: dbquery.ColumnFilterKindRange, scale: 1},
		{value: f.SizeKB, expression: "cp.size_in_bytes", kind: dbquery.ColumnFilterKindRange, scale: 1024},
		{value: f.UnusedFor, expression: "DATEDIFF_BIG(MILLISECOND, qs.last_execution_time, SYSDATETIME())", kind: dbquery.ColumnFilterKindDuration, scale: 1, includeNull: true},
	} {
		var err error
		query, err = applyPlanRange(query, selection)
		if err != nil {
			return nil, err
		}
	}

	return query, nil
}

func resolvePaging(f Filter) (int, int, error) {
	limit := f.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return 0, 0, fmt.Errorf("--limit must be positive, got %d", limit)
	case limit > MaxLimit:
		return 0, 0, fmt.Errorf("--limit %d exceeds the maximum of %d; narrow the filter instead", limit, MaxLimit)
	}
	if f.Offset < 0 {
		return 0, 0, fmt.Errorf("--offset must be non-negative, got %d", f.Offset)
	}
	return limit, f.Offset, nil
}

// List returns one page of cached plans matching f.
func List(ctx context.Context, db *gorm.DB, f Filter) ([]CachedPlan, error) {
	query, err := baseQuery(db.WithContext(ctx), f)
	if err != nil {
		return nil, err
	}
	limit, offset, err := resolvePaging(f)
	if err != nil {
		return nil, err
	}
	if query, err = Sort.Apply(query.Select(selectList), f.SortOptions); err != nil {
		return nil, err
	}

	var rows []cachedPlanRow
	if err := query.Limit(limit).Offset(offset).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list cached plans: %w", err)
	}
	out := make([]CachedPlan, len(rows))
	for i, row := range rows {
		out[i] = row.toCachedPlan()
	}
	return out, nil
}

func ListPaged(ctx context.Context, db *gorm.DB, f Filter) ([]CachedPlan, int64, error) {
	total, err := CountMatching(ctx, db, f)
	if err != nil {
		return nil, 0, err
	}
	rows, err := List(ctx, db, f)
	return rows, total, err
}

// CountMatching counts the entire filtered set before limit and offset are
// applied. Filtered destructive actions use it as their optimistic guard.
func CountMatching(ctx context.Context, db *gorm.DB, f Filter) (int64, error) {
	query, err := baseQuery(db.WithContext(ctx), f)
	if err != nil {
		return 0, err
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count cached plans: %w", err)
	}
	return total, nil
}

// Get returns one cached plan by handle. It reuses the listing's projection so
// the detail view and the row can never disagree about a value.
func Get(ctx context.Context, db *gorm.DB, planHandle string) (*CachedPlan, error) {
	// IncludeStubs is set because a caller naming an exact handle has already
	// chosen the row; silently refusing to return a stub it can see in the
	// listing would be the surprise.
	plans, err := List(ctx, db, Filter{PlanHandles: []string{planHandle}, IncludeStubs: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, fmt.Errorf("plan handle %s is not in the cache", planHandle)
	}
	return &plans[0], nil
}

// SelectHandles returns just the plan handles matching f — the shared selection
// behind the eviction preview and the eviction itself, so the two can never
// resolve different sets.
func SelectHandles(ctx context.Context, db *gorm.DB, f Filter) ([]string, error) {
	query, err := baseQuery(db.WithContext(ctx), f)
	if err != nil {
		return nil, err
	}
	limit, offset, err := resolvePaging(f)
	if err != nil {
		return nil, err
	}
	if query, err = Sort.Apply(query.Select("cp.plan_handle AS plan_handle"), f.SortOptions); err != nil {
		return nil, err
	}

	var rows []struct {
		PlanHandle []byte `gorm:"column:plan_handle"`
	}
	if err := query.Limit(limit).Offset(offset).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("resolve plan handles: %w", err)
	}
	handles := make([]string, 0, len(rows))
	for _, row := range rows {
		if encoded := EncodeHandle(row.PlanHandle); encoded != "" {
			handles = append(handles, encoded)
		}
	}
	return handles, nil
}
