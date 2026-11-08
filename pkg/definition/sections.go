package definition

import "github.com/flanksource/telltale/pkg/traces"

// Each typed list carries the kind it compiles to in its telltale tag
// (kind=<id>), and may fix the seam (seam=<seam>) or the logs backend
// (backend=<provider>). A group's tag gives the seam its lists are aimed at
// unless a list fixes its own. The tags are read by the walker (walk.go);
// the field's json name is the path segment and the default-name part.

// Proxy are the proxies a command is routed through, each on its own port
// and exporting its own variables.
type Proxy struct {
	HTTP     []ProxyHTTP     `json:"http,omitempty" clicky:"title=HTTP proxies" telltale:"kind=http,seam=proxy"`
	Postgres []ProxyPostgres `json:"postgres,omitempty" clicky:"title=Postgres proxies" telltale:"kind=sql,seam=proxy"`
}

// Instrument are the captures that change the target's runtime: they attach,
// inject, start recorders or create server objects.
type Instrument struct {
	Go        InstrumentGo        `json:"go,omitzero" clicky:"title=Go" telltale:"seam=process"`
	JVM       InstrumentJVM       `json:"jvm,omitzero" clicky:"title=JVM" telltale:"seam=process"`
	SQLServer InstrumentSQLServer `json:"sqlserver,omitzero" clicky:"title=SQL Server" telltale:"seam=connection"`
}

type InstrumentGo struct {
	PProf []InstrumentPProf `json:"pprof,omitempty" clicky:"title=pprof,desc=Mutex and block profiles: sets the Go runtime's profile rate" telltale:"kind=goprof"`
}

type InstrumentJVM struct {
	FlightRecorder []FlightRecorder `json:"flightRecorder,omitempty" clicky:"title=Flight Recorder,desc=Starts a JFR recording" telltale:"kind=jvm_jfr"`
	AsyncProfiler  []AsyncProfiler  `json:"asyncProfiler,omitempty" clicky:"title=async-profiler,desc=Attaches async-profiler" telltale:"kind=jvm_jfr"`
}

type InstrumentSQLServer struct {
	XEvent []XEvent `json:"xevent,omitempty" clicky:"title=Extended Events,desc=Creates a server-scoped Extended Events session" telltale:"kind=sql_xevent"`
}

// Collect are the captures that only read: they never change the target, its
// runtime or the servers it reaches.
type Collect struct {
	Go         CollectGo         `json:"go,omitzero" clicky:"title=Go" telltale:"seam=process"`
	Process    CollectProcess    `json:"process,omitzero" clicky:"title=Process" telltale:"seam=process"`
	Logs       CollectLogs       `json:"logs,omitzero" clicky:"title=Logs" telltale:"seam=host"`
	Query      CollectQuery      `json:"query,omitzero" clicky:"title=Query" telltale:"seam=host"`
	Host       CollectHost       `json:"host,omitzero" clicky:"title=Host" telltale:"seam=host"`
	Prometheus CollectPrometheus `json:"prometheus,omitzero" clicky:"title=Prometheus" telltale:"seam=host"`
	OpenSearch CollectOpenSearch `json:"opensearch,omitzero" clicky:"title=OpenSearch" telltale:"seam=host"`
	SQLServer  CollectSQLServer  `json:"sqlserver,omitzero" clicky:"title=SQL Server" telltale:"seam=connection"`
	Telltale   CollectTelltale   `json:"telltale,omitzero" clicky:"title=Telltale" telltale:"seam=self"`
}

// CollectGo reads a Go process: over its gops agent or pprof endpoint, or
// through a connected telltale Go agent.
type CollectGo struct {
	Gops  []Gops         `json:"gops,omitempty" clicky:"title=gops,desc=Goroutine dumps over the process's gops agent" telltale:"kind=stacks"`
	PProf []CollectPProf `json:"pprof,omitempty" clicky:"title=pprof,desc=Heap cpu allocs or goroutine profiles" telltale:"kind=goprof"`
	HTTP  []HTTPProbe    `json:"http,omitempty" clicky:"title=HTTP" telltale:"kind=http,seam=agent"`
	SQL   []SQLProbe     `json:"sql,omitempty" clicky:"title=SQL" telltale:"kind=sql,seam=agent"`
	Exec  []ExecProbe    `json:"exec,omitempty" clicky:"title=Executions" telltale:"kind=exec,seam=agent"`
	LLM   []LLMProbe     `json:"llm,omitempty" clicky:"title=LLM calls" telltale:"kind=llm,seam=agent"`
	Logs  []LogsProbe    `json:"logs,omitempty" clicky:"title=Logs" telltale:"kind=logs,seam=agent"`
	Spans []SpansProbe   `json:"spans,omitempty" clicky:"title=Spans" telltale:"kind=spans,seam=agent"`
}

// CollectProcess reads the execution's process tree.
type CollectProcess struct {
	// Tree is the run-wide process tree every command or pid run records; it
	// is one object, not a capture list, and absent reads no facets.
	Tree         *traces.TreeOptions `json:"tree,omitempty" clicky:"title=Process tree,desc=What the run-wide process tree reads of each process"`
	NativeStacks []NativeStacks      `json:"nativeStacks,omitempty" clicky:"title=Native stacks,desc=Thread stacks of any process: sample (darwin) eu-stack or gdb (linux)" telltale:"kind=stacks"`
	Resources    []Resources         `json:"resources,omitempty" clicky:"title=Processes,desc=Startup metadata resource changes and metrics" telltale:"kind=process"`
}

// CollectLogs reads log sources outside the process, by the backend that
// serves them.
type CollectLogs struct {
	Kubernetes        []KubernetesLogs `json:"kubernetes,omitempty" clicky:"title=Kubernetes" telltale:"kind=logs"`
	Container         []ContainerLogs  `json:"container,omitempty" clicky:"title=Container" telltale:"kind=logs"`
	Loki              []CloudLogs      `json:"loki,omitempty" clicky:"title=Loki" telltale:"kind=logs,backend=loki"`
	Cloudwatch        []CloudLogs      `json:"cloudwatch,omitempty" clicky:"title=CloudWatch" telltale:"kind=logs,backend=cloudwatch"`
	OpenSearch        []CloudLogs      `json:"opensearch,omitempty" clicky:"title=OpenSearch" telltale:"kind=logs,backend=opensearch"`
	GCPCloudLogging   []CloudLogs      `json:"gcpCloudLogging,omitempty" clicky:"title=GCP Cloud Logging" telltale:"kind=logs,backend=gcpcloudlogging"`
	AzureLogAnalytics []CloudLogs      `json:"azureLogAnalytics,omitempty" clicky:"title=Azure Log Analytics" telltale:"kind=logs,backend=azureloganalytics"`
}

// CollectQuery reads commons-db query providers over the run's window, by
// provider, or a saved profile by name.
type CollectQuery struct {
	SQL           []SQLQuery           `json:"sql,omitempty" clicky:"title=SQL" telltale:"kind=query,provider=sql"`
	Prometheus    []PrometheusQuery    `json:"prometheus,omitempty" clicky:"title=Prometheus" telltale:"kind=query,provider=prometheus"`
	OpenSearch    []OpenSearchQuery    `json:"opensearch,omitempty" clicky:"title=OpenSearch" telltale:"kind=query,provider=opensearch"`
	HTTP          []HTTPQuery          `json:"http,omitempty" clicky:"title=HTTP" telltale:"kind=query,provider=http"`
	PostgREST     []PostgRESTQuery     `json:"postgrest,omitempty" clicky:"title=PostgREST" telltale:"kind=query,provider=postgrest"`
	BigQuery      []BigQueryQuery      `json:"bigquery,omitempty" clicky:"title=BigQuery" telltale:"kind=query,provider=bigquery"`
	Jaeger        []JaegerQuery        `json:"jaeger,omitempty" clicky:"title=Jaeger" telltale:"kind=query,provider=jaeger"`
	OpenTelemetry []OpenTelemetryQuery `json:"opentelemetry,omitempty" clicky:"title=OpenTelemetry" telltale:"kind=query,provider=opentelemetry"`
	// CloudWatchMetrics is named cloudwatchMetrics, apart from collect.logs.cloudwatch, which reads CloudWatch Logs.
	CloudWatchMetrics []CloudWatchMetricsQuery `json:"cloudwatchMetrics,omitempty" clicky:"title=CloudWatch metrics" telltale:"kind=query,provider=cloudwatchmetrics"`
	Profile           []ProfileQuery           `json:"profile,omitempty" clicky:"title=Saved profile" telltale:"kind=query"`
}

type CollectHost struct {
	Processes []Resources `json:"processes,omitempty" clicky:"title=Processes" telltale:"kind=process"`
}

type CollectPrometheus struct {
	Scrape []Scrape `json:"scrape,omitempty" clicky:"title=Scrape" telltale:"kind=prometheus"`
}

type CollectOpenSearch struct {
	Spans []OpenSearchSpans `json:"spans,omitempty" clicky:"title=Spans" telltale:"kind=spans"`
}

type CollectSQLServer struct {
	SystemHealth   []SystemHealth   `json:"systemHealth,omitempty" clicky:"title=system_health,desc=Attach read-only to the system_health session" telltale:"kind=sql_xevent"`
	PlanCache      []PlanCache      `json:"planCache,omitempty" clicky:"title=Plan cache" telltale:"kind=sql_plancache"`
	DeadlockReport []DeadlockReport `json:"deadlockReport,omitempty" clicky:"title=Deadlock report" telltale:"kind=sql_deadlock"`
}

// CollectTelltale reads telltale's own process.
type CollectTelltale struct {
	HTTP     []HTTPProbe     `json:"http,omitempty" clicky:"title=HTTP" telltale:"kind=http"`
	SQL      []SQLProbe      `json:"sql,omitempty" clicky:"title=SQL" telltale:"kind=sql"`
	Exec     []ExecProbe     `json:"exec,omitempty" clicky:"title=Executions" telltale:"kind=exec"`
	LLM      []LLMProbe      `json:"llm,omitempty" clicky:"title=LLM calls" telltale:"kind=llm"`
	Logs     []LogsProbe     `json:"logs,omitempty" clicky:"title=Logs" telltale:"kind=logs"`
	Spans    []SpansProbe    `json:"spans,omitempty" clicky:"title=Spans" telltale:"kind=spans"`
	Terminal []TerminalProbe `json:"terminal,omitempty" clicky:"title=Terminal" telltale:"kind=terminal"`
}
