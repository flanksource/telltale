# SQL Server and JVM captures

`sql_xevent`, `sql_deadlock`, and `jvm_jfr` are built-in trace kinds. They use the same run stores, browser start form, result tables, filters, checkpoints, and exports as other captures. Build with `make build` to include the Java agent JAR in the binary.

## SQL Server

Configure a SQL Server connection using the [connection configuration](https://github.com/flanksource/telltale#connections) and pass its `connection://name` reference. These captures need neither a local PID nor a command. An empty database filter selects the connection's database; `--databases '*'` selects all databases. Filters accept exact names, `*` wildcards, and `!` exclusions.

```sh
telltale run sql_xevent --connection connection://orders --session-name diagnostic --min-duration 10ms --duration 30s
telltale run sql_xevent --connection connection://orders --session system_health --duration 30s
telltale run sql_deadlock --connection connection://orders --duration 1m --poll 5s
telltale run sql_deadlock --connection connection://orders --from 2026-10-28T08:00:00Z --to 2026-10-28T09:00:00Z
```

For managed XEvents, `--session-name` is required. Telltale appends a unique suffix, drains the capture, and drops only that owned session. `--events` selects events; `--users`, `--apps`, `--hosts`, and `--databases` scope the server predicate. `--types` and `--tables` filter decoded statements. `--poll`, `--max-events`, and `--max-memory-kb` control polling and ring-buffer capacity. Lost or truncated events are reported.

`--session system_health` attaches to the existing event files from their current end. It leaves the session and its configuration running. Statement capture requires a managed session; `system_health` contains only the events SQL Server already records there. Managed sessions require permission to create and alter server event sessions. Reading existing sessions needs `VIEW SERVER STATE`, or `VIEW SERVER PERFORMANCE STATE` on SQL Server 2022+, and permission to read the event files.

Deadlock capture reads `system_health` without changing it. With no `--from`, it follows from the SQL Server clock and records the instance deadlock-counter delta. On stop, a nonzero delta makes it wait for the configured dispatch latency plus two seconds before the final file read; shutdown can therefore take longer than `--duration`. The counter is instance-wide, while database filters apply to graph participants. A counter reset fails the capture. A server with unbounded dispatch latency is refused.

`--from` starts a historical read that completes automatically. Timestamps are RFC 3339, the start is inclusive, and `--to` is exclusive; an omitted end uses server time. Only retained event files are available. Graphs are deduplicated by identity, include victim and participant statements, and retain the XML and decode error when a graph cannot be decoded.

## Local JVM

```sh
telltale run jvm_jfr --pid 1234 --duration 30s --every 10s
telltale run jvm_jfr --pid 1234 --engine async-profiler --event wall --interval 10ms --duration 30s
```

The process seam requires a compatible JDK's `jcmd` on `PATH`, Attach API permission, and a JVM with Flight Recorder. Java 8 needs a distribution with the JFR backport, such as 8u262 or newer; Java 11+ provides it directly. The async-profiler process seam requires `asprof` on `PATH`, or `--async-profiler /absolute/path/asprof`.

`--engine jfr` uses `--settings profile` by default. `--engine async-profiler` requires `--event cpu`, `wall`, `alloc`, or `lock`; `--interval` controls sampling. `--threads` retains thread identities and `--max-stack-depth` bounds decoded stacks. Telltale owns its recording and refuses to take over an already active async-profiler. Checkpoints decode samples newer than each recording's watermark; the final recording is drained before shutdown.

## Java agent installation

The Java agent supports Java 8 with JFR and Java 11+. It bundles async-profiler libraries for Linux x64/arm64 and macOS. Native profiling needs the platform permissions required by the selected event. The installer uses a JDK with Attach API; for Java 8, set `--java-home` to the target-compatible JDK so its `tools.jar` is included. Dynamic attachment must be enabled and the installer must have permission to attach to the target JVM.

```sh
telltale agents install java --pid 1234 --directory /home/example/.local/share/telltale --java-home /opt/jdk --service orders
telltale run jvm_jfr --seam agent --pid 1234 --duration 30s --every 10s
```

Installation stages a versioned artifact under the chosen absolute directory, verifies its SHA-256 on the destination, rechecks process identity, and invokes the Java Attach helper. Local discovery files and staged credentials are private. Repeating a compatible local install reuses the authenticated agent; an incompatible agent requires restarting the JVM. `--dry-run` returns the destination and artifact hash without copying or attaching.

Remote agents dial out to a Telltale server. Create an agent-scope token as the user that runs the hub (it is minted into the global config dir, `~/.config/telltale/agent-tokens`), save the printed value in a private file, and give that file to the installer with `--token-file`. The token value is never a command argument. The hub URL must use HTTP or HTTPS and be reachable both from the target JVM and the installer. Use HTTPS outside a trusted development network. If the hub protects its agent-list API, provide an API-scope token through `TELLTALE_API_TOKEN` to verify readiness.

```sh
telltale agents token create java-runtime --scope agent
telltale serve --host 0.0.0.0 --port 8080 --data-dir /srv/telltale
telltale agents install java --destination ssh --host app-host --pid 1234 --directory /opt/telltale --java-home /opt/jdk --hub-url https://telltale.example.com --token-file /home/example/.config/telltale/java-token --service orders
telltale agents install java --destination kubernetes --context development --namespace shop --pod orders-0 --container java --pid 1 --directory /opt/telltale --java-home /opt/java/openjdk --hub-url https://telltale.example.com --token-file /home/example/.config/telltale/java-token --service orders
telltale agents list --server https://telltale.example.com
```

SSH uses native SSH configuration, batch authentication, and optionally `--ssh-config`. Kubernetes uses native kubeconfig and requires explicit namespace, pod, and container. Destinations require POSIX `ps`, `install`, `dd`, `mv`, `rm`, `uname`, and `sha256sum` (or `shasum` on macOS). The installer copies and attaches the agent; profiling then uses the agent protocol.

Start remote captures in the connected hub's browser, selecting the agent seam and an agent ID or resource selector such as `service.name=orders`. Remote agent identities are namespaced and cannot be confused with a controller's local PID. A JVM disconnect releases its recordings; the dial-out connection retries with bounded backoff. Recording blobs are limited to 64 MiB.

## HTTP API and browser

`GET /api/traces/start` includes schemas and defaults for all three kinds. `POST /api/traces` accepts `pid`, `duration`, and `every`; `command` is optional for connection or agent captures. For example:

```json
{"duration":"30s","every":"10s","traces":{"jvm_jfr":{"enabled":true,"target":{"seam":"agent","selector":{"service.name":"orders"}},"engine":"jfr","settings":"profile"}}}
```

```json
{"traces":{"sql_deadlock":{"enabled":true,"connection":"connection://orders","from":"2026-10-28T08:00:00Z","to":"2026-10-28T09:00:00Z"}}}
```

Use the run's existing result/export routes to read stored rows and the checkpoint route for JFR. The generated `/api/openapi.json` also exposes `agents install java` and the single-kind run commands. Installer API calls execute on the server and therefore use its filesystem, SSH configuration, and kubeconfig.

## Verification

`make test` builds the shaded agent and runs Java, Go, and frontend tests. Java compatibility CI runs on 8, 11, 17, and 21. Set `COMMONS_DB_SQLSERVER_URL` only to a disposable SQL Server to run the live fixtures: they create/drop an XE session and a table and deliberately generate a deadlock.

```sh
go test ./pkg/tracing/sqlserver/... ./pkg/tracing/jfr ./pkg/agentinstall
```

The hub installer fixture normally launches its own local JVM. `TELLTALE_JVM_FIXTURE_DESTINATION` can explicitly select a disposable SSH destination or a Kubernetes pod running `pkg/agentinstall/testdata/Workload.java` as PID 1. Set `TELLTALE_JVM_FIXTURE_HUB_HOST` to a controller address reachable by that JVM, and use an absolute `KUBECONFIG` for an isolated cluster. Never point these fixture variables at application workloads.
