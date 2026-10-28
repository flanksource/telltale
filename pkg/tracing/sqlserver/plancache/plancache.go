// Package plancache samples SQL Server cached-plan execution counters.
package plancache

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type CachedPlan struct {
	// PlanHandle is the hex-encoded varbinary(64) handle, rendered with the
	// 0x prefix SQL Server itself prints. It is the eviction key.
	PlanHandle    string `json:"planHandle"`
	QueryHash     string `json:"queryHash,omitempty"`
	QueryPlanHash string `json:"queryPlanHash,omitempty"`

	Database string `json:"database,omitempty"`
	// Object is the procedure/trigger/view the plan belongs to. Empty for an
	// adhoc or prepared batch, which has no object.
	Object string `json:"object,omitempty"`

	// ObjType is Adhoc | Prepared | Proc | Trigger | View | UsrTab.
	ObjType string `json:"objType"`
	// CacheObjType is Compiled Plan | Compiled Plan Stub | Parse Tree | Extended
	// Proc. Only a Compiled Plan carries a showplan.
	CacheObjType string `json:"cacheObjType"`

	// UseCounts is how many times the cache entry has been looked up — cache
	// reuse, which is NOT the same as ExecutionCount below.
	UseCounts int64 `json:"useCounts"`
	SizeBytes int64 `json:"sizeBytes"`

	ExecutionCount int64 `json:"executionCount"`

	TotalCPU     time.Duration `json:"totalCpu"`
	AvgCPU       time.Duration `json:"avgCpu"`
	TotalElapsed time.Duration `json:"totalElapsed"`
	AvgElapsed   time.Duration `json:"avgElapsed"`

	TotalLogicalReads  int64 `json:"totalLogicalReads"`
	AvgLogicalReads    int64 `json:"avgLogicalReads"`
	TotalPhysicalReads int64 `json:"totalPhysicalReads"`
	TotalWrites        int64 `json:"totalWrites"`
	TotalRows          int64 `json:"totalRows"`

	CreationTime      time.Time `json:"creationTime,omitempty"`
	LastExecutionTime time.Time `json:"lastExecutionTime,omitempty"`

	// SQL is the whole batch text the plan was compiled from. For a procedure
	// that is the entire body; per-statement attribution lives on the detail
	// view, not the listing.
	SQL string `json:"sql,omitempty"`
}

type cachedPlanRow struct {
	PlanHandle   []byte  `gorm:"column:plan_handle"`
	ObjType      string  `gorm:"column:obj_type"`
	CacheObjType string  `gorm:"column:cache_obj_type"`
	UseCounts    int64   `gorm:"column:use_counts"`
	SizeBytes    int64   `gorm:"column:size_bytes"`
	Database     *string `gorm:"column:database_name"`
	Object       *string `gorm:"column:object_name"`
	SQL          *string `gorm:"column:sql_text"`

	ExecutionCount     *int64 `gorm:"column:execution_count"`
	TotalWorkerMicros  *int64 `gorm:"column:total_worker_time"`
	TotalElapsedMicros *int64 `gorm:"column:total_elapsed_time"`
	TotalLogicalReads  *int64 `gorm:"column:total_logical_reads"`
	TotalPhysicalReads *int64 `gorm:"column:total_physical_reads"`
	TotalWrites        *int64 `gorm:"column:total_writes"`
	TotalRows          *int64 `gorm:"column:total_rows"`

	CreationTime      *time.Time `gorm:"column:creation_time"`
	LastExecutionTime *time.Time `gorm:"column:last_execution_time"`

	QueryHash     []byte `gorm:"column:query_hash"`
	QueryPlanHash []byte `gorm:"column:query_plan_hash"`
}

func (r cachedPlanRow) toCachedPlan() CachedPlan {
	executions := derefInt64(r.ExecutionCount)
	plan := CachedPlan{
		PlanHandle:         EncodeHandle(r.PlanHandle),
		QueryHash:          EncodeHandle(r.QueryHash),
		QueryPlanHash:      EncodeHandle(r.QueryPlanHash),
		Database:           derefString(r.Database),
		Object:             derefString(r.Object),
		ObjType:            r.ObjType,
		CacheObjType:       r.CacheObjType,
		UseCounts:          r.UseCounts,
		SizeBytes:          r.SizeBytes,
		ExecutionCount:     executions,
		TotalCPU:           micros(r.TotalWorkerMicros),
		TotalElapsed:       micros(r.TotalElapsedMicros),
		TotalLogicalReads:  derefInt64(r.TotalLogicalReads),
		TotalPhysicalReads: derefInt64(r.TotalPhysicalReads),
		TotalWrites:        derefInt64(r.TotalWrites),
		TotalRows:          derefInt64(r.TotalRows),
		CreationTime:       derefTime(r.CreationTime),
		LastExecutionTime:  derefTime(r.LastExecutionTime),
		SQL:                derefString(r.SQL),
	}
	// Averages are derived, not stored: SQL Server keeps only the totals. They
	// are computed here as well as in SQL (where sorting needs them) so the two
	// never disagree by rounding.
	if executions > 0 {
		plan.AvgCPU = plan.TotalCPU / time.Duration(executions)
		plan.AvgElapsed = plan.TotalElapsed / time.Duration(executions)
		plan.AvgLogicalReads = plan.TotalLogicalReads / executions
	}
	return plan
}

// EncodeHandle renders a varbinary handle the way SQL Server prints it, so a
// value copied out of the listing pastes straight back into a filter. An empty
// handle encodes to "" rather than a bare "0x", which would parse back as a
// zero-length handle that matches nothing.
func EncodeHandle(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return "0x" + strings.ToUpper(hex.EncodeToString(raw))
}

// DecodeHandle parses a 0x-prefixed (or bare) hex handle back to the varbinary
// SQL Server expects. It fails loudly on malformed input: a handle silently
// coerced to empty would turn a targeted eviction into one that matches nothing
// and reports success.
func DecodeHandle(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "0X"), "0x")
	if trimmed == "" {
		return nil, fmt.Errorf("empty plan handle")
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("plan handle %q is not hex: %w", value, err)
	}
	return raw, nil
}

func micros(value *int64) time.Duration {
	if value == nil {
		return 0
	}
	return time.Duration(*value) * time.Microsecond
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
