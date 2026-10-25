package traces

import (
	"github.com/flanksource/telltale/pkg/httpx"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/pgproxy"
	"github.com/flanksource/telltale/pkg/tracing/process"
	tracesql "github.com/flanksource/telltale/pkg/tracing/sql"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/terminal"
)

// The builtin kinds, registered before any command or run reads the catalog.
// The `http` and `sql_statements` kinds default to telltale's own traffic:
// its HTTP feature and its own database. goprof reads gops agents, at the
// address their port files give.
func init() {
	for _, registration := range []Registration{
		{
			Name: httpproxy.Kind, Title: "HTTP proxy", Target: TargetCommand, Armable: true,
			Summary: "Record the command's HTTP and HTTPS traffic through a local proxy",
			Description: "Starts a recording proxy and points the command at it through HTTP_PROXY and HTTPS_PROXY. " +
				"In connect mode an HTTPS tunnel is recorded by host, duration and bytes, its payload left encrypted. " +
				"In mitm mode the proxy terminates TLS with an ephemeral CA the command is told to trust through the CA variables, recording each exchange in full. " +
				"Plain HTTP is recorded in full in either mode. A program that ignores proxy variables is not recorded.",
			Exports: []string{
				"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy",
				"SSL_CERT_FILE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE",
			},
			Examples: []Example{
				{Args: "-- curl -sS https://example.com", Says: "record the tunnel to example.com"},
				{Args: "--mode mitm -- curl -sS https://example.com", Says: "decrypt and record the exchange in full"},
				{Args: "--mode mitm --hosts '*.example.com' --bodies 0 -- make test", Says: "decrypt only example.com hosts and keep no bodies"},
			},
			New: func(KindDeps) tracing.TracePlugin { return httpproxy.NewKind() },
		},
		{
			Name: pgproxy.Kind, Title: "Postgres proxy", Target: TargetCommand, Armable: true,
			Summary: "Record the SQL statements the command sends to Postgres through a local proxy",
			Description: "Starts a Postgres wire proxy in front of the upstream DSN and hands the command a DSN pointing at it, " +
				"through DATABASE_URL, GAVEL_DB_DSN, PGHOST and PGPORT, or the variable --env names. " +
				"Each statement is recorded with its duration and rows; bind values only with --params.",
			Exports: []string{"DATABASE_URL", "GAVEL_DB_DSN", "PGHOST", "PGPORT", "PGSSLMODE"},
			Examples: []Example{
				{Args: "--dsn postgres://app@localhost:5432/orders -- ./migrate up", Says: "record the migration's statements"},
				{Args: "--env ORDERS_DB_URL --params -- go test ./...", Says: "proxy the DSN the tests read from ORDERS_DB_URL and keep bind values"},
			},
			New: func(KindDeps) tracing.TracePlugin { return pgproxy.NewKind() },
		},
		{
			Name: process.TreeKind, Title: "Process tree", Target: TargetProcess,
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
			Name: process.SamplesKind, Title: "Process samples", Target: TargetProcess,
			Summary: "Sample the CPU, memory, disk IO and open descriptors of every process of a tree",
			Description: "Samples each process under its root at every interval: telltale's own tree, which holds the command, or the process --pid attaches to. " +
				"Facets add each process's open files, sockets, listening ports or connections, read with lsof.",
			Examples: []Example{
				{Args: "--facets ports,connections -- ./server", Says: "sample the server and the ports and connections it opens"},
				{Args: "--pid 1234 --interval 250ms --duration 1m", Says: "sample process 1234's tree four times a second for a minute"},
			},
			New: func(deps KindDeps) tracing.TracePlugin { return process.NewSamplesKind(deps.Sampler) },
		},
		{
			Name: groups.Kind, Title: "Process groups", Target: TargetHost,
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
			Name: stacks.Kind, Title: "Stacks", Target: TargetProcess,
			Checkpoint: stacks.CheckpointParams{Reason: "interval"},
			Summary:    "Dump the stacks of a process at each checkpoint",
			Description: "Dumps the goroutines of a Go process, or the threads of any other with the OS sampler, at each checkpoint: " +
				"every --every, and as an attached run stops. A run of a command dumps the command; --pid dumps that process.",
			Examples: []Example{
				{Args: "--every 10s -- ./server", Says: "dump the server's stacks every 10 seconds"},
				{Args: "--pid 1234 --duration 5s", Says: "dump process 1234's stacks once as the run stops"},
			},
			New: func(deps KindDeps) tracing.TracePlugin { return stacks.NewKind(deps.Dumpers) },
		},
		{
			Name: goprof.Kind, Title: "Go profiles", Target: TargetProcess,
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
			Name: "http", Title: "Telltale HTTP", Target: TargetSelf,
			Summary: "Record telltale's own outbound HTTP, not the command's",
			Description: "Records the HTTP exchanges telltale itself makes over its instrumented transports, such as connection lookups. " +
				"It sees nothing the command sends: use http_proxy for that.",
			Examples: []Example{{Args: "--features http -- true", Says: "record telltale's own HTTP while true runs"}},
			New: func(KindDeps) tracing.TracePlugin {
				return tracehttp.Kind().WithDefaults(tracehttp.Params{Features: []string{httpx.Feature}})
			},
		},
		{
			Name: tracesql.RowKind, Title: "Telltale SQL", Target: TargetSelf,
			Summary: "Record the SQL statements telltale's own connections run, not the command's",
			Description: "Records the statements telltale runs over the connections it opens itself. " +
				"It sees nothing the command sends: use pg_proxy for that.",
			Examples: []Example{{Args: "--connections self -- true", Says: "record telltale's own database statements"}},
			New: func(KindDeps) tracing.TracePlugin {
				return tracesql.Kind().WithDefaults(tracesql.Params{Connections: []string{"self"}})
			},
		},
		{
			Name: terminal.Kind, Title: "Terminal", Target: TargetSelf,
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
