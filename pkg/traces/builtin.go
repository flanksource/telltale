package traces

import (
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	tracesql "github.com/flanksource/telltale/pkg/tracing/sql"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

// The builtin kinds, registered before any command or run reads the catalog.
// The `http` and `sql` kinds default to telltale's own traffic:
// its HTTP feature and its own database. goprof reads gops agents, at the
// address their port files give.
func init() {
	for _, registration := range []Registration{
		{
			Name: process.TreeKind, Title: "Process tree", Seams: []target.Seam{target.Process}, DefaultSeam: target.Process,
			Summary: "Record every process of a tree as it starts, changes and exits",
			Description: "Follows the process tree under its root: telltale's own tree, which holds the command, or the process --pid attaches to. " +
				"Every run records this tree already; arm it again for another interval or to read each process's environment.",
			Examples: []Example{
				{Args: "--environment-prefixes AWS_ -- make deploy", Says: "record the tree with each process's AWS_ variables"},
				{Args: "--pid 1234 --duration 30s", Says: "follow process 1234's tree for 30 seconds"},
			},
			New: func(deps KindDeps) tracing.TracePlugin {
				return process.NewTreeKind(deps.Sampler, process.TreeOptions{Dumpers: deps.Dumpers})
			},
		},
		{
			Name: process.SamplesKind, Title: "Process samples", Seams: []target.Seam{target.Process}, DefaultSeam: target.Process,
			Summary: "Sample the CPU, memory, disk IO and open descriptors of every process of a tree",
			Description: "Samples each process under its root at every interval: telltale's own tree, which holds the command, or the process --pid attaches to. " +
				"Facets add each process's open files, sockets, listening ports or connections, read with lsof.",
			Examples: []Example{
				{Args: "--facets ports,connections -- ./server", Says: "sample the server and the ports and connections it opens"},
				{Args: "--seam process --pid 1234 --interval 250ms --duration 1m", Says: "sample process 1234's tree four times a second for a minute"},
			},
			New: func(deps KindDeps) tracing.TracePlugin { return process.NewSamplesKind(deps.Sampler) },
		},
		{
			Name: groups.Kind, Title: "Process groups", Seams: []target.Seam{target.Host}, DefaultSeam: target.Host,
			Summary: "Record the CPU, memory, disk, sockets and network of each tool's processes on the host",
			Description: "Sorts every process on the host into the tool that owns it — claude, codex, cmux, gavel, go test, vitest, agent-browser, chrome and more — " +
				"as a tree of groups, each with its working directory, checkout, agent session and cmux workspace, and records what each group and those under it use. " +
				"Rules come from the built-in catalog and .telltale/groups.yaml. Network bytes are read with nettop on darwin only when asked for, as it costs over a core.",
			Examples: []Example{
				{Args: "--duration 10m", Says: "record every tool's groups for ten minutes"},
				{Args: "--groups claude,vitest --net", Says: "record claude sessions and vitest runs with their network bytes"},
			},
			New: func(deps KindDeps) tracing.TracePlugin { return groups.NewKind(deps.Sampler) },
		},
		{
			Name: stacks.Kind, Title: "Stacks", Seams: []target.Seam{target.Process, target.Agent, target.Self}, DefaultSeam: target.Process,
			Checkpoint: stacks.CheckpointParams{Reason: "interval"},
			Summary:    "Dump the stacks of a process at each checkpoint",
			Description: "Dumps the goroutines of a Go process, or the threads of any other with the OS sampler, at each checkpoint: " +
				"every --every, and as an attached run stops. A run of a command dumps the command; --pid dumps that process.",
			Examples: []Example{
				{Args: "--every 10s -- ./server", Says: "dump the server's stacks every 10 seconds"},
				{Args: "--pid 1234 --duration 5s", Says: "dump process 1234's stacks once as the run stops"},
			},
			New: func(deps KindDeps) tracing.TracePlugin { return stacks.NewKind(nil, deps.Dumpers) },
		},
		{
			Name: goprof.Kind, Title: "Go profiles", Seams: []target.Seam{target.Process, target.Agent, target.Self}, DefaultSeam: target.Process,
			Checkpoint: goprof.CheckpointParams{Source: goprof.SourceGops, Profile: "heap", Reason: "interval"},
			Summary:    "Read the heap profile of a Go process over its gops agent at each checkpoint",
			Description: "Reads a pprof profile of a Go process that runs a gops agent (github.com/google/gops/agent) at each checkpoint: " +
				"every --every, and as an attached run stops. The agent is found from the port file it writes. " +
				"A run of a command profiles the command; --pid profiles that process.",
			Examples: []Example{
				{Args: "--every 30s -- ./server", Says: "read the server's heap every 30 seconds"},
				{Args: "--pid 1234 --duration 1m --every 15s", Says: "read process 1234's heap every 15 seconds for a minute"},
			},
			New: func(KindDeps) tracing.TracePlugin { return goprof.NewKind(goprof.Addresses{Gops: stacks.GopsAddress}) },
		},
		{
			Name: "http", Title: "HTTP", Seams: []target.Seam{target.Self, target.Agent, target.Proxy}, DefaultSeam: target.Self, Armable: true,
			Summary:     "Record HTTP exchanges from an agent or a local proxy",
			Description: "Observes instrumented HTTP in telltale or an agent, or routes a command through a recording proxy.",
			Exports:     []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE"},
			Examples:    []Example{{Args: "--features http -- true", Says: "record telltale HTTP"}, {Args: "--seam proxy --mode mitm -- curl -sS https://example.com", Says: "record a command through the proxy"}},
			New:         func(KindDeps) tracing.TracePlugin { return httpproxy.UnifiedKind(nil) },
		},
		{
			Name: tracesql.RowKind, Title: "SQL", Seams: []target.Seam{target.Self, target.Agent, target.Proxy}, DefaultSeam: target.Self, Armable: true,
			Summary:     "Record SQL statements from an agent or a local Postgres proxy",
			Description: "Observes instrumented connections in telltale or an agent, or routes a command through a Postgres proxy.",
			Exports:     []string{"DATABASE_URL", "GAVEL_DB_DSN", "PGHOST", "PGPORT", "PGSSLMODE"},
			Examples:    []Example{{Args: "--connections self -- true", Says: "record telltale's database statements"}, {Args: "--seam proxy --dsn postgres://app@localhost:5432/orders -- ./migrate up", Says: "record a command through the Postgres proxy"}},
			New:         func(KindDeps) tracing.TracePlugin { return pgproxy.UnifiedKind(nil) },
		},
		{
			Name: terminal.Kind, Title: "Terminal", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self,
			Summary: "Record terminal events a host feeds in, not the command's output",
			Description: "Records the terminal events a host such as a test runner feeds the capture. " +
				"A run of a command feeds it none, so it records nothing; the command's output is the run's own output.",
			Examples: []Example{{Args: "-- true", Says: "arm an empty terminal stream"}},
			New:      func(KindDeps) tracing.TracePlugin { return terminal.NewKind() },
		},
	} {
		MustRegister(registration)
	}
}
