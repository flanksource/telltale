# Record every trace kind

Run this from the repository root to create one ordinary persisted Telltale run with fresh recordings for every kind returned by `traces.Registrations()`. The program exits unsuccessfully if any kind is missing, has no useful records, loses records, fails to stop, or fails cleanup. It never loads recorded fixtures.

## Prerequisites

- Run `make setup` and use the repository's local Go workspace.
- Put `git`, `java`, `javac`, `jcmd`, and `ps` on `PATH`. Use a JDK that supports Flight Recorder and allows attaching to a child JVM.
- Configure `connection://mssql` for **mssql.lab:31433**, with permission to create/drop a database, create/drop Extended Events sessions, inspect plan-cache DMVs, and read `system_health` deadlock events. This demo uses the lab server, not Docker. It creates only a uniquely named `telltale_demo_*` database and XE session and removes them after the captures drain.
- Authenticate a Captain API model with known pricing. Supply the model explicitly. One fresh, short request is made per recording, with a 128-token output limit, no tools or caching, and a maximum approved cost of USD 0.05. No LLM request is made by tests or `--verify-run`.

To save the existing lab Connection resource in the shared connection store, run this once:

```sh
go run ./hack/demo --save-connection-from "$HOME/.config/sqlglot-go/connections/lab.yaml"
```

The helper validates the endpoint, normalizes the database to `master`, saves `mssql.yaml` with mode `0600`, and refuses to overwrite an existing shared connection. Credentials stay in that connection file and memory; trace params contain `connection://mssql`.

## Record and inspect

```sh
go run ./hack/demo --model api:claude-haiku-5-5
```

Use any authenticated Captain API model whose bounded request fits the cap. `--data-dir` defaults to `.tmp/demo/data`; `--timeout` defaults to `2m`. Progress goes to stderr and the final JSON report goes to stdout. The report includes the run ID, store directory, native record counts, final session summaries, and errors. It is also saved as `demo-report.json` in the run directory. The workload's SQLite database, Java source/classes, and JVM log remain in a sibling `work-*` directory for inspection.

Recheck a saved recording without another paid request:

```sh
go run ./hack/demo --verify-run <run-id>
```

After `make build`, view the saved records using the local binary:

```sh
.bin/telltale trace replay <run-id> --data-dir .tmp/demo/data --format ndjson
.bin/telltale trace replay <run-id> --data-dir .tmp/demo/data --format ansi
.bin/telltale trace replay <run-id> --data-dir .tmp/demo/data --tui
.bin/telltale trace replay <run-id> --data-dir .tmp/demo/data --ui
```

## Recorded work

| Kinds | Real workload |
| --- | --- |
| `http`, `sql`, `logs`, `spans` | Instrumented localhost orders requests reading a freshly seeded SQLite database |
| `exec`, `terminal` | A Clicky execution and a separate PTY execution of `git --version`, including output, resize, and measured exit status |
| `stacks`, `goprof` | Periodic native stack and heap readings of the demo process while it holds allocations |
| `process`, `process/metrics`, `process/facets` | Native process identities, numeric observations, and resource events during the workloads |
| `jvm_jfr` | A child JVM allocating, computing, parking, and contending while Flight Recorder samples it |
| `prometheus`, `query` | Managed Prometheus scraping the demo's request counter and a windowed query reading those samples |
| `sql_xevent`, `sql_plancache`, `sql_deadlock` | Lab database statements, execution deltas, and two transactions updating rows in opposite order to produce a deadlock |
| `llm` | One observed Captain API request and its actual response/token usage |

The run is stopped and drained before the JVM, local HTTP server, and lab database are removed. Errors and partial traces are retained. A new catalog kind requires a new real workload and evidence predicate; it cannot silently pass.

## Clicky UI event playground

Export representative events through the same Clicky pretty printers used by CLI follow and replay, without another LLM call or external SQL workload:

```sh
CLICKY_FORCE_INTERACTIVE=1 go run ./hack/demo --verify-run <run-id> --export-playground ../clicky-ui/apps/playground/src/pages/telltale/_trace-events.json
```

The sibling Clicky UI playground page is `?page=telltale%2Ftrace-events`. Styled log rows are the primary view; Details, Native view, and Stored record tabs expose the full presentation and original payload. HTTP, SQL, XEvents, and execution rows reuse the existing shared formatting code. All registered kinds and named result tables are included, with variants such as JFR CPU/allocation/lock/park, LLM request/response, terminal output/resize/snapshot/exit, and SQL Extended Events.

When the source recording predates the current process schema, the exporter records a three-second local process capture with an observed file descriptor. These process, metrics, and facet examples identify their own source run. Other examples retain their original recording, payloads, and sequence numbers. Counts describe the complete source streams; opening the playground requires no running capture services.
