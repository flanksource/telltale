package snapshot

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/tracing/jvm/jvmruntime"
)

// workloadPage renders workload.json with the published clicky-ui WorkloadPanel.
//
//go:embed workload.html
var workloadPage []byte

// workloadMarker is the page's empty data island; workloadHTML fills it.
const workloadMarker = `<script type="application/json" id="workload">null</script>`

// workloadInterval is the time between workload samples.
var workloadInterval = 5 * time.Second

// WorkloadTarget names the container the workload panel describes.
type WorkloadTarget struct {
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Container string `json:"container"`
}

// workloadTarget reads the Kubernetes resource attributes an agent reports.
func workloadTarget(resource map[string]string, service string) WorkloadTarget {
	target := WorkloadTarget{Name: resource["telltale.deployer"], Namespace: resource["k8s.namespace.name"], Container: resource["k8s.container.name"]}
	if pod := resource["k8s.pod.name"]; target.Name == "" && pod != "" {
		target.Kind, target.Name = "pod", pod
	}
	if target.Name == "" {
		target.Name = service
	}
	if target.Name == "" {
		target.Name = "jvm"
	}
	if target.Container == "" {
		target.Container = target.Name
	}
	return target
}

// workloadSample is one poll of the container and JVM the panel summarises.
type workloadSample struct {
	At          time.Time `json:"at"`
	CPUCores    float64   `json:"cpuCores"`
	CPULimit    float64   `json:"cpuLimit"`
	Memory      int64     `json:"memory"`
	MemoryLimit int64     `json:"memoryLimit"`
	Eden        int64     `json:"eden"`
	Survivor    int64     `json:"survivor"`
	Old         int64     `json:"old"`
	HeapMax     int64     `json:"heapMax"`
	Metaspace   int64     `json:"metaspace"`
	CodeCache   int64     `json:"codeCache"`
	Direct      int64     `json:"direct"`
	GCCount     int64     `json:"gcCount"`
	GCTimeMs    int64     `json:"gcTimeMs"`
	Live        int       `json:"live"`
	Daemon      int       `json:"daemon"`
	Blocked     int       `json:"blocked"`
}

func sampleWorkload(ctx context.Context, call caller) (workloadSample, error) {
	raw := map[string]json.RawMessage{}
	for _, op := range []string{jvmruntime.MetricsOp, jvmruntime.ResourcesOp, jvmruntime.ThreadsOp} {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		data, err := call(bounded, op)
		cancel()
		if err != nil {
			return workloadSample{}, fmt.Errorf("%s: %w", op, err)
		}
		raw[op] = data
	}
	metrics, err := jvmruntime.DecodeMetrics(raw[jvmruntime.MetricsOp])
	if err != nil {
		return workloadSample{}, err
	}
	resources, err := jvmruntime.DecodeResources(raw[jvmruntime.ResourcesOp])
	if err != nil {
		return workloadSample{}, err
	}
	threads, err := jvmruntime.DecodeThreads(raw[jvmruntime.ThreadsOp])
	if err != nil {
		return workloadSample{}, err
	}
	return newWorkloadSample(metrics, resources, threads)
}

// newWorkloadSample reads container usage from the cgroup v2 files and JVM
// usage from its memory pools. Heap pools that are neither eden nor survivor
// (old, tenured, or a single-generation ZGC/Shenandoah heap) count as old.
func newWorkloadSample(metrics jvmruntime.Metrics, resources jvmruntime.Resources, threads jvmruntime.ThreadSample) (workloadSample, error) {
	if metrics.ProcessCPUPercent == nil {
		return workloadSample{}, fmt.Errorf("%s has no processCpuPercent", jvmruntime.MetricsOp)
	}
	cgroup := resources.Cgroups["/sys/fs/cgroup"]
	if cgroup == nil {
		return workloadSample{}, fmt.Errorf("%s has no cgroup v2 at /sys/fs/cgroup; the workload panel needs a container's cgroup", jvmruntime.ResourcesOp)
	}
	processors := float64(metrics.Runtime.Processors)
	sample := workloadSample{At: metrics.CapturedAt, CPUCores: *metrics.ProcessCPUPercent / 100 * processors, CPULimit: processors}
	cpuMax := strings.Fields(cgroup["cpu.max"])
	if len(cpuMax) != 2 {
		return workloadSample{}, fmt.Errorf("cgroup cpu.max %q: want \"<quota> <period>\"", cgroup["cpu.max"])
	}
	if cpuMax[0] != "max" {
		quota, quotaErr := strconv.ParseFloat(cpuMax[0], 64)
		period, periodErr := strconv.ParseFloat(cpuMax[1], 64)
		if quotaErr != nil || periodErr != nil || period <= 0 {
			return workloadSample{}, fmt.Errorf("cgroup cpu.max %q: want numeric quota and period", cgroup["cpu.max"])
		}
		sample.CPULimit = quota / period
	}
	var err error
	if sample.Memory, err = strconv.ParseInt(strings.TrimSpace(cgroup["memory.current"]), 10, 64); err != nil {
		return workloadSample{}, fmt.Errorf("cgroup memory.current: %w", err)
	}
	if limit := strings.TrimSpace(cgroup["memory.max"]); limit != "max" {
		if sample.MemoryLimit, err = strconv.ParseInt(limit, 10, 64); err != nil {
			return workloadSample{}, fmt.Errorf("cgroup memory.max: %w", err)
		}
	}
	for _, pool := range metrics.Memory.Heap {
		name := strings.ToLower(pool.Name)
		switch {
		case strings.Contains(name, "eden"):
			sample.Eden += pool.Used
		case strings.Contains(name, "survivor"):
			sample.Survivor += pool.Used
		default:
			sample.Old += pool.Used
		}
		sample.HeapMax += max(0, pool.Max)
	}
	for _, pool := range metrics.Memory.NonHeap {
		name := strings.ToLower(pool.Name)
		switch {
		case name == "metaspace":
			sample.Metaspace += pool.Used
		case strings.Contains(name, "code"):
			sample.CodeCache += pool.Used
		}
	}
	for _, pool := range metrics.Memory.Buffer {
		if pool.Name == "direct" {
			sample.Direct += pool.Used
		}
	}
	for _, collector := range metrics.GarbageCollectors {
		sample.GCCount += collector.CollectionCount
		sample.GCTimeMs += collector.CollectionTime
	}
	sample.Live = len(threads.Threads)
	for _, thread := range threads.Threads {
		if thread.Daemon {
			sample.Daemon++
		}
		if thread.State == "BLOCKED" {
			sample.Blocked++
		}
	}
	return sample, nil
}

// statistics is the clicky-ui WorkloadResourceStatistics: CPU in cores, memory in bytes.
type statistics struct {
	Average float64 `json:"average"`
	P90     float64 `json:"p90"`
	P95     float64 `json:"p95"`
	Limit   float64 `json:"limit"`
}

type gcStatistics struct {
	Count      int64   `json:"count"`
	TimeMs     int64   `json:"timeMs"`
	P90Ms      float64 `json:"p90Ms"`
	P95Ms      float64 `json:"p95Ms"`
	MaxMs      float64 `json:"maxMs"`
	PercentP90 float64 `json:"percentP90"`
	PercentP95 float64 `json:"percentP95"`
}

// workloadPanel is the clicky-ui WorkloadPanelProps the page renders.
type workloadPanel struct {
	Workload            map[string]any `json:"workload"`
	Container           string         `json:"container"`
	ObservationWindowMs int64          `json:"observationWindowMs"`
	CPU                 statistics     `json:"cpu"`
	Memory              statistics     `json:"memory"`
	JVM                 struct {
		Heap    statistics         `json:"heap"`
		Regions map[string]float64 `json:"regions"`
		GC      gcStatistics       `json:"gc"`
		Threads map[string]int     `json:"threads"`
	} `json:"jvm"`
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	return sorted[max(0, int(math.Ceil(p/100*float64(len(sorted))))-1)]
}

func summarise(samples []workloadSample, value func(workloadSample) float64, limit float64) statistics {
	values := make([]float64, len(samples))
	sum := 0.0
	for i, sample := range samples {
		values[i] = value(sample)
		sum += values[i]
	}
	return statistics{Average: sum / float64(len(values)), P90: percentile(values, 90), P95: percentile(values, 95), Limit: limit}
}

// newWorkloadPanel summarises samples over their window. GC pause figures are
// the average pause within each sampling interval, since the JVM only exposes
// cumulative collection counts and times.
func newWorkloadPanel(target WorkloadTarget, samples []workloadSample) (workloadPanel, error) {
	if len(samples) < 2 {
		return workloadPanel{}, fmt.Errorf("the workload panel needs at least 2 samples, got %d", len(samples))
	}
	first, last := samples[0], samples[len(samples)-1]
	panel := workloadPanel{
		Workload:            map[string]any{"name": target.Name, "status": map[string]string{"label": "Running", "health": "healthy"}},
		Container:           target.Container,
		ObservationWindowMs: last.At.Sub(first.At).Milliseconds(),
		CPU:                 summarise(samples, func(s workloadSample) float64 { return s.CPUCores }, last.CPULimit),
		Memory:              summarise(samples, func(s workloadSample) float64 { return float64(s.Memory) }, float64(last.MemoryLimit)),
	}
	if panel.ObservationWindowMs <= 0 {
		return workloadPanel{}, fmt.Errorf("workload samples span %s; want a positive window", last.At.Sub(first.At))
	}
	if target.Kind != "" {
		panel.Workload["kind"] = target.Kind
	}
	if target.Namespace != "" {
		panel.Workload["namespace"] = target.Namespace
	}
	average := func(value func(workloadSample) int64) float64 {
		return summarise(samples, func(s workloadSample) float64 { return float64(value(s)) }, 0).Average
	}
	panel.JVM.Regions = map[string]float64{
		"eden":      average(func(s workloadSample) int64 { return s.Eden }),
		"survivor":  average(func(s workloadSample) int64 { return s.Survivor }),
		"old":       average(func(s workloadSample) int64 { return s.Old }),
		"metaspace": average(func(s workloadSample) int64 { return s.Metaspace }),
		"codeCache": average(func(s workloadSample) int64 { return s.CodeCache }),
		"direct":    average(func(s workloadSample) int64 { return s.Direct }),
	}
	panel.JVM.Heap = summarise(samples, func(s workloadSample) float64 { return float64(s.Eden + s.Survivor + s.Old) }, float64(last.HeapMax))
	regions := 0.0
	for _, bytes := range panel.JVM.Regions {
		regions += bytes
	}
	if regions > panel.Memory.Average {
		return workloadPanel{}, fmt.Errorf("JVM memory regions average %.0f bytes, more than the container's %.0f", regions, panel.Memory.Average)
	}
	var pauses, percents []float64
	for i, sample := range samples[1:] {
		count, timeMs := sample.GCCount-samples[i].GCCount, sample.GCTimeMs-samples[i].GCTimeMs
		if count < 0 || timeMs < 0 {
			return workloadPanel{}, fmt.Errorf("GC totals went backwards between %s and %s; the JVM restarted", samples[i].At, sample.At)
		}
		if count == 0 {
			continue
		}
		panel.JVM.GC.Count += count
		panel.JVM.GC.TimeMs += timeMs
		pauses = append(pauses, float64(timeMs)/float64(count))
		percents = append(percents, min(100, float64(timeMs)/float64(max(1, sample.At.Sub(samples[i].At).Milliseconds()))*100))
	}
	if len(pauses) > 0 {
		panel.JVM.GC.P90Ms, panel.JVM.GC.P95Ms, panel.JVM.GC.MaxMs = percentile(pauses, 90), percentile(pauses, 95), slices.Max(pauses)
		panel.JVM.GC.PercentP90, panel.JVM.GC.PercentP95 = percentile(percents, 90), percentile(percents, 95)
	}
	peak := 0
	for _, sample := range samples {
		peak = max(peak, sample.Live)
	}
	panel.JVM.Threads = map[string]int{"live": last.Live, "daemon": last.Daemon, "blocked": last.Blocked, "peak": peak}
	return panel, nil
}

// collectWorkload samples count times, workloadInterval apart.
func collectWorkload(ctx context.Context, call caller, count int) ([]workloadSample, error) {
	var samples []workloadSample
	for i := range count {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(workloadInterval):
			}
		}
		sample, err := sampleWorkload(ctx, call)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

// workloadHTML fills the page's data island; json.Marshal escapes <, > and &,
// so no value can close the script element.
func workloadHTML(panel workloadPanel) ([]byte, error) {
	if !bytes.Contains(workloadPage, []byte(workloadMarker)) {
		return nil, fmt.Errorf("workload.html has no %s", workloadMarker)
	}
	data, err := json.Marshal(panel)
	if err != nil {
		return nil, err
	}
	island := strings.Replace(workloadMarker, ">null<", ">"+string(data)+"<", 1)
	return bytes.Replace(workloadPage, []byte(workloadMarker), []byte(island), 1), nil
}
