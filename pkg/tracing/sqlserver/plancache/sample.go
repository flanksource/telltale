package plancache

import (
	"fmt"
	"time"
)

type Sample struct {
	SampledAt       time.Time `json:"sampledAt" pretty:"label=Sampled" sort:"sampledAt"`
	IntervalMs      float64   `json:"intervalMs" pretty:"label=Interval,type=duration,unit=ms" sort:"intervalMs"`
	PlanHandle      string    `json:"planHandle" pretty:"label=Plan handle" filter:"exact"`
	QueryHash       string    `json:"queryHash" filter:"exact"`
	QueryPlanHash   string    `json:"queryPlanHash" filter:"exact"`
	Database        string    `json:"database" filter:"terms"`
	Object          string    `json:"object" filter:"terms"`
	ObjType         string    `json:"objType" filter:"terms"`
	Executions      int64     `json:"executions" sort:"executions"`
	CPUMs           float64   `json:"cpuMs" pretty:"label=CPU,type=duration,unit=ms" sort:"cpuMs"`
	ElapsedMs       float64   `json:"elapsedMs" pretty:"label=Elapsed,type=duration,unit=ms" sort:"elapsedMs"`
	LogicalReads    int64     `json:"logicalReads" sort:"logicalReads"`
	PhysicalReads   int64     `json:"physicalReads" sort:"physicalReads"`
	Writes          int64     `json:"writes" sort:"writes"`
	Rows            int64     `json:"rows" sort:"rows"`
	AvgElapsedMs    float64   `json:"avgElapsedMs" pretty:"label=Average elapsed,type=duration,unit=ms" sort:"avgElapsedMs"`
	AvgCPUMs        float64   `json:"avgCpuMs" pretty:"label=Average CPU,type=duration,unit=ms" sort:"avgCpuMs"`
	TotalExecutions int64     `json:"totalExecutions" sort:"totalExecutions"`
	TotalCPUMs      float64   `json:"totalCpuMs" pretty:"label=Total CPU,type=duration,unit=ms" sort:"totalCpuMs"`
	TotalElapsedMs  float64   `json:"totalElapsedMs" pretty:"label=Total elapsed,type=duration,unit=ms" sort:"totalElapsedMs"`
	UseCounts       int64     `json:"useCounts" sort:"useCounts"`
	SizeBytes       int64     `json:"sizeBytes" pretty:"type=bytes" sort:"sizeBytes"`
	CreationTime    time.Time `json:"creationTime" sort:"creationTime"`
	Recompiled      bool      `json:"recompiled" filter:"terms"`
	Evicted         bool      `json:"evicted" filter:"terms"`
	SQL             string    `json:"sql" pretty:"label=SQL" filter:"text"`
}

func planSample(p CachedPlan) Sample {
	return Sample{PlanHandle: p.PlanHandle, QueryHash: p.QueryHash, QueryPlanHash: p.QueryPlanHash,
		Database: p.Database, Object: p.Object, ObjType: p.ObjType, SQL: p.SQL,
		TotalExecutions: p.ExecutionCount, TotalCPUMs: float64(p.TotalCPU) / float64(time.Millisecond),
		TotalElapsedMs: float64(p.TotalElapsed) / float64(time.Millisecond),
		UseCounts:      p.UseCounts, SizeBytes: p.SizeBytes, CreationTime: p.CreationTime}
}

// Delta refuses counter regressions unless creation time identifies a recompile.
// An empty previous handle identifies a plan first observed after the baseline.
func Delta(prev, cur CachedPlan) (Sample, bool, error) {
	if cur.PlanHandle == "" || (prev.PlanHandle != "" && prev.PlanHandle != cur.PlanHandle) {
		return Sample{}, false, fmt.Errorf("delta requires the same nonempty plan handle: previous %q, current %q", prev.PlanHandle, cur.PlanHandle)
	}
	s := planSample(cur)
	s.Recompiled = prev.PlanHandle != "" && !prev.CreationTime.Equal(cur.CreationTime)
	before := []int64{prev.ExecutionCount, int64(prev.TotalCPU), int64(prev.TotalElapsed), prev.TotalLogicalReads, prev.TotalPhysicalReads, prev.TotalWrites, prev.TotalRows}
	after := []int64{cur.ExecutionCount, int64(cur.TotalCPU), int64(cur.TotalElapsed), cur.TotalLogicalReads, cur.TotalPhysicalReads, cur.TotalWrites, cur.TotalRows}
	names := []string{"executions", "cpu", "elapsed", "logical reads", "physical reads", "writes", "rows"}
	for i := range after {
		if after[i] < 0 || before[i] < 0 {
			return Sample{}, false, fmt.Errorf("plan %s: negative %s counter", cur.PlanHandle, names[i])
		}
		if s.Recompiled || prev.PlanHandle == "" {
			before[i] = 0
		}
		if after[i] < before[i] {
			return Sample{}, false, fmt.Errorf("plan %s: %s regressed from %d to %d", cur.PlanHandle, names[i], before[i], after[i])
		}
		after[i] -= before[i]
	}
	if after[0] == 0 {
		return s, false, nil
	}
	s.Executions = after[0]
	s.CPUMs, s.ElapsedMs = float64(after[1])/float64(time.Millisecond), float64(after[2])/float64(time.Millisecond)
	s.LogicalReads, s.PhysicalReads, s.Writes, s.Rows = after[3], after[4], after[5], after[6]
	s.AvgCPUMs, s.AvgElapsedMs = s.CPUMs/float64(s.Executions), s.ElapsedMs/float64(s.Executions)
	return s, true, nil
}
