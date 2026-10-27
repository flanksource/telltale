# Telltale

A Flanksource Go CLI and HTTP server with a React frontend built with Vite and Clicky UI.

## Setup

Install Go 1.27.1, Node 24, pnpm 11.22.0, [Task](https://taskfile.dev/installation/), Gavel, and actionlint. The root `packageManager` field pins pnpm; enable your package manager's version management before installing.

```sh
make setup
make build
.bin/telltale serve
```

Open `http://localhost:8080`. The server exposes `/health` and `/api/openapi.json`. Use `--host` and `--port` to change the API listener; port `0` requests a free port. `--version` reports the build version, which defaults to `dev` outside releases.

## Tracing a command

`telltale trace run` runs a command under trace and keeps what it recorded in a run store at `.telltale/traces/<run id>` under `--work-dir` (default `.`). Each `--trace kind[:{json params}]` arms one capture and points the command at it through the environment it exports; the default is the `http_proxy` recording proxy. The process tree under telltale is always recorded.

```sh
# Record TLS traffic in full: the proxy terminates TLS with an ephemeral CA it exports to curl.
telltale trace run --trace 'http_proxy:{"mode":"mitm"}' -- curl -sS https://example.com
telltale trace list --status failed --since 2h
telltale serve
```

`telltale trace list` filters runs by `--query` (command, run id or `pid N`), `--status` (`running`, `succeeded`, `failed`, `unfinished`), `--kind`, `--since` and `--limit`. The 20 newest runs are kept. Set `-Phttp.har=<path>` on any command to capture telltale's own outbound HTTP as a HAR archive. `telltale trace run --help` lists every trace kind with its params.

### One kind at a time, or a running process

`telltale run <kind>` arms a single trace kind, its params as flags. There is one subcommand for every kind in the registry, which `pkg/traces` fills in `init()` before the CLI is built, so a kind registered by another package's `init()` gets its own command with no CLI change. `telltale run <kind> --help` shows what the kind records, its flags with their defaults and choices, the variables it exports and examples.

```sh
telltale run http_proxy --mode mitm --hosts '*.example.com' -- curl -sS https://api.example.com
telltale run pg_proxy --env ORDERS_DB_URL --params -- go test ./...
telltale run stacks --every 10s -- ./server          # dump the server's stacks every 10s
telltale run process_samples --pid 1234 --facets ports --duration 1m
```

Kinds that observe a process tree (`process_tree`, `process_samples`, `stacks`, `goprof`) can attach to a running process with `--pid`, recording until Ctrl+C, `--duration`, or the process exits; the process is left running. Proxies (`http_proxy`, `pg_proxy`) only work on a command they are pointed at. `stacks` and `goprof` read at checkpoints: every `--every`, and once as an attached run stops. A checkpoint that fails fails the run, listed under `checkpointErrors`.

Presets are named runs of several kinds, each a top-level command:

| Command | Records |
|---|---|
| `telltale ps <pid>` | the process tree with each process's CPU, memory, disk IO, open files, sockets, ports, connections and environment |
| `telltale gops <pid>` | the goroutines and heap profile of a Go process running a [gops agent](https://github.com/google/gops), every 30s; the agent is found from its port file (`$GOPS_CONFIG_DIR/<pid>`) |

Both also take `-- command [args...]`. A program registers its own with `telltale.RegisterPreset` in `init()`.

`telltale serve` mounts the runs of `--work-dir` at `/api/traces`, and the web UI starts, stops, filters and shows them, live while they run:

| Route | Does |
|---|---|
| `GET /api/traces?q=&status=&kind=&since=&limit=` | Lists runs, newest first (`since` is RFC 3339) |
| `GET /api/traces/start` | The start form: a JSON schema and its defaults |
| `POST /api/traces` | Starts `{"command": "...", "facets": false, "traces": {"http_proxy": {"enabled": true, "mode": "mitm"}}}`; the command runs with `sh -c` in the work dir and its output goes to `output.log` in the run's store. A refused request is a 400 `{"errors": [{"instancePath", "message"}]}` |
| `DELETE /api/traces/<run id>` | Stops a run the server is running |
| `/api/traces/<run id>/api/v1/...` | The run's records, exports and checkpoints, live from its running captures |

`POST` and `DELETE` run commands, so they take only same-origin requests, and `POST` takes only an `application/json` body.

### Process groups: `telltale top`

`telltale top` sorts every process on the host into the tool that owns it and shows what each one uses, refreshed in place every `--every` (default 2s). A process a rule matches starts a group under the group of its nearest ancestor that has one, so a `go test` that gavel runs for a claude session shows as `cmux ▸ claude ▸ gavel ▸ go-test`. Every other process joins its nearest ancestor's group, and a process matching the same rule as that group joins it rather than starting a new one: a test binary joins its `go test`, and Chrome's helpers join Chrome. Each group shows its total (itself plus the groups under it) for processes, CPU % (rated over at least a second), RSS, disk read and write per second, network in and out per second, and sockets. It also shows where the group runs: its checkout and branch, the claude or codex session it runs under, and its cmux workspace and surface, each by name. A session is found in its anchor's environment, on a claude command line (`--session-id`, `--resume`), or, for codex, in the rollout file it holds open. Its title is the one captain gives it (the agent's own title, its slug, or its opening prompt), read from its transcript in the background and again once a minute when the transcript has changed. cmux's workspace and surface titles come from `cmux tree`.

```sh
telltale top                                   # live, nothing kept
telltale top --groups claude,vitest --sort mem # only these groups and those under them
telltale top --all                             # also the processes no tool holds: other ▸ <program>, system ▸ <program>
telltale top --net                             # with network bytes (nettop, darwin)
telltale top --once --format json              # one frame, once CPU can be rated
telltale top --record --duration 10m           # record into .telltale/traces
telltale trace list --kind process_groups
```

Use `--group-by repo`, `--group-by worktree`, or `--group-by claude-session` to collect tool groups into expandable buckets; `--group-by tree` is the default process tree. Repository grouping combines a main checkout and its linked worktrees using their shared Git directory, while worktree grouping keeps each checkout separate and shows its branch. Claude session grouping uses the full session ID and displays its title when available; tools inherit their ancestor's session until another agent session begins. Missing metadata stays visible under `Unattributed`. Bucket totals count each tool group's own usage once and preserve unknown measurements.

Press `b` in the terminal to cycle grouping without restarting sampling. The web `/top` page offers the same modes through **Group by**, persists the choice in `topGroupBy`, and applies it to tables and trends. Recordings retain raw tool groups, so grouping can change during playback; older recordings without shared Git directory metadata remain unattributed in repository mode, while their recorded checkout paths still support worktree grouping. For example, `telltale top --group-by repo --once --format json` prints one grouped frame, and `telltale top --group-by worktree --record --duration 1m` records raw groups while showing worktree buckets in an interactive terminal.

In a terminal, `telltale top` takes the whole screen. The header shows the host's CPU split between processes (user), the kernel (system) and idle time and load, bars for memory and swap, and the disks' read and write IOPS and throughput. The title ends with telltale's own overhead: the CPU it and the commands it runs each tick (`ps`, `lsof`, `launchctl`) use, and how long a tick takes; JSON frames carry it as `overhead`. The groups fill the rest of the window as a tree, and the origin and context columns appear only when the window is wide enough. Keys: `↑↓` (or `j`/`k`, `g`/`G`, PgUp/PgDn) move; `←→` and space fold; `enter` lists the selected group's processes below the tree; `s` cycles the sort column and `S` reverses it; `/` filters by name, command, repo, owner or launchd label; `o` shows the processes no tool holds; `n` reads network bytes; `q` quits. When stdout is not a terminal, or with `--once`, it prints one frame instead.

CPU hogs are highlighted when their CPU use exceeds 25% of all logical cores: on an 8-core host, the threshold is strictly greater than 200% in the per-core CPU column. In the terminal, open the process list with `enter`, choose a process with `[` / `]`, then press `x` to send SIGTERM or `r` to set its nice value to 10; `y` confirms and `n` / Escape cancels. The web process table offers **Terminate** and **Renice**, with a confirmation showing the PID and command and a nice value from -20 to 19 (higher means lower priority). These controls act on one live process, require OS permissions, and reject a process whose start-qualified identity changed. Recordings remain read-only.

`POST /api/top/action` accepts an `application/json` body such as `{"key":"123@1793000000","action":"terminate"}` or `{"key":"123@1793000000","action":"renice","nice":10}`. Use the key returned by the live processes endpoint. Success is 204; invalid input is 400, stale identity is 409, and OS permission failures are 403. Cross-origin browser requests are refused. PID 1 and the Telltale server itself cannot be targeted.

Each group also says where its process came from (`origin`):
- the owner, and the scope: `system` for root and the role accounts, `user` otherwise;
- the launchd job that started it, read from `launchctl print system` and `launchctl print gui/<uid>` (no root needed): `brew-service` for `homebrew.mxcl.<name>`, `app` for `application.<bundle id>`, `apple` for `com.apple.*`, `launchd` for any other job;
- `brew` for a program under brew's prefix, and `apple` for a daemon launchd started from `/System`, `/usr/libexec`, `/usr/sbin` or `/sbin`.

A brew service no rule matches becomes a `service` group of its own, so `postgresql@17` holds its backends. A program inside an app bundle in an `Applications` folder (`/Applications`, `/System/Applications`, `~/Applications` or a folder in one) that no rule matches joins its app's `app` group, under `applications` unless a tool started it, so every helper of an Electron app adds up in one place. The telltale reading the host is a `telltale` group of its own, with what it keeps running such as `nettop`. The processes no tool holds sit under `other` (the user's) and `system`, grouped by program. The kernel itself (`kernel_task`) is not a process `ps` lists, so its time shows as the host's kernel CPU share.

Nothing is kept unless you pass `--record`, which starts a host run: a run with neither a command nor a process, arming the `process_groups` trace kind (also `telltale run process_groups`). The screen then shows the frames as the run's store commits them. On darwin, network bytes come from `nettop`, which uses more than a core however long its interval, so it runs only with `--net`; other platforms refuse `--net`. Socket and connection counts come from `lsof` on every platform.

The built-in rules (`pkg/tracing/groups/catalog.yaml`) cover claude, codex, cmux, captain, gavel, go test, vitest, playwright, agent-browser, lightpanda, chrome, vite, make, pnpm/npm and gopls. Rules in `.telltale/groups.yaml` under `--work-dir`, or in the file `--catalog` names, go ahead of the built-in ones. A rule with a built-in rule's name replaces it, and `disabled: true` drops it:

```yaml
groups:
  - name: acme-api          # a new rule, matched before the built-in ones
    kind: service           # agent, browser, test, build, tool, service or app
    target: positional      # what the group runs against: positional, session, or a flag such as --session
    match:                  # any one match; a match needs all of its fields
      - exec: [acme-api]    # globs over the program's base name
      - contains: ["/acme/bin/api.js"]
        exclude: [" --dry-run"]
  - name: gopls
    disabled: true
```

`telltale serve` serves the latest live frame, with the host's use, at `GET /api/top?all=true&net=true`, and keeps reading it every 2s while it is asked for. `GET /api/top/processes?name=<program>` returns the processes running a program, and `?pid=<n>` a process and every process under it. The web UI's Top page (`/top`) shows the groups live with gauges for the host, and recorded `process_groups` runs over time. `/top/<program>` and `/top/<pid>` show one program's or one process tree's processes, with gauges and their history. `GET /api/top/facets?pid=<n>` reads a process the latest frame listed, and answers 404 for one it did not. It returns the process's context with those names, its environment with its secrets masked as stored traces mask them, and its open files, sockets, listening ports and connections: the facets `process_samples` and `process_tree` record. `/top/<pid>` shows these facets for its process every 5s, and clicking a process in the table shows that process's.

### Go API

The module root package starts, stops, lists and serves runs for other Go programs:

```go
run, err := telltale.Start(ctx, telltale.StartOptions{
	WorkDir: ".", Command: []string{"curl", "-sS", "https://example.com"},
	Traces:    []telltale.TraceSpec{{Kind: "http_proxy"}},
	TraceRoot: telltale.TraceRootCommand, // trace the command's own tree, in its own process group
})
result, err := run.Wait() // or run.Stop(), or telltale.Stop(telltale.StopOptions{WorkDir: ".", RunID: run.ID()})

runs, err := telltale.List(telltale.ListOptions{WorkDir: ".", Status: []telltale.Status{telltale.StatusFailed}, Limit: 10})

// Mount the routes above in your own server; Close stops the runs it started.
connections, err := telltale.OpenConnections() // or store.Open("your-app"), or store.New(layers...)
handler, err := telltale.NewHandler(telltale.HandlerOptions{WorkDir: ".", Prefix: "/api/traces", Connections: connections})
mux.Handle("/api/traces", handler)
mux.Handle("/api/traces/", handler)
defer handler.Close()
```

`StartOptions.PID` attaches to a running process in place of `Command`, `Duration` stops a run, and `Every` sets how often checkpointed captures are read. `traces.Register` adds a trace kind to the catalog every run, the CLI and the start form read; its `Target` says whether the kind is pointed at by a command, observes a process tree, or records telltale itself. `telltale.StartSchema()` returns the start form's JSON schema and defaults, and `telltale.StartRequest.Options` turns a submitted form into `StartOptions`, refusing bad fields with a `*telltale.ValidationError`. The form renders with Clicky UI's `JsonSchemaForm`.

### Connections

Captures that reach another system name a connection, such as `connection://shop/orders`. Telltale reads connections from commons-db's file store (`connection/store`), which has three layers. A later layer overrides an earlier one's connection of the same namespace and name:

1. `~/.config/flanksource/connections`, shared by every Flanksource tool
2. `~/.config/telltale/connections`, telltale's own
3. `./.connections`, the working directory's

`$XDG_CONFIG_HOME` replaces `~/.config`. Each file is one typed mission-control `Connection` resource, the same YAML you would `kubectl apply`:

```yaml
apiVersion: mission-control.flanksource.com/v1
kind: Connection
metadata: {name: orders, namespace: shop}
spec:
  mssql:
    host: {value: "db:1433"}
    database: {value: orders}
    username: {value: app}
    password: {valueFrom: {onePassword: "op://ops/orders/password"}}
```

Credentials can be references (`valueFrom.secretKeyRef`, `configMapKeyRef`, `onePassword`), which are resolved when a capture connects. Keep connection files readable by you alone (`chmod 600`).

```sh
telltale connection list --type sql_server
telltale connection get orders
```

`GET /api/traces/connections?type=` lists the same connections over the API, without URLs or credentials.

The other Go packages are importable too:

| Package | Purpose |
|---|---|
| `pkg/tracing` and its kinds (`http`, `httpproxy`, `pgproxy`, `sql`, `process`, `groups`, `stacks`, `goprof`, `terminal`, `jfr`, `opensearch`, `sqlserver/*`) | Trace plugin framework and capture kinds |
| `pkg/traces` | Per-run trace store host |
| `pkg/traceserve` | Opens a trace store and serves its read, export and checkpoint routes |
| `pkg/proxy/{credproxy,kubeproxy,mcpproxy,proxylog}` | Credential-injecting egress proxy, Kubernetes and MCP reverse proxies, JSONL request log |
| `pkg/credentials` (vault), `pkg/credentials/{agentcreds,credsync,apitoken}` | Provider key vault, agent CLI logins, credential publishing, bearer tokens |

## Development with local Clicky UI

Install the sibling Clicky UI checkout's dependencies using its own setup instructions, then start Telltale:

```sh
make dev
```

This runs `telltale serve --dev`, starts Vite on `http://localhost:5173`, and proxies `/api` and `/health` to Go. Open the Vite URL for HMR. The Go URL also proxies frontend requests. Ctrl+C stops both servers. Go changes require restarting the command.

`--dev` resolves the sibling `../clicky-ui/packages/ui/src` directly, including public component entrypoints, styles, and theme configuration. It reports an error when the selected source checkout is missing. To use another Clicky UI worktree:

```sh
TELLTALE_CLICKY_UI_DIR=/path/to/clicky-ui task dev
```

Set `--ui-port` to change the Vite port and `--web-dir` when launching outside the repository root:

```sh
task dev -- --port 8180 --ui-port 5174
```

To install a binary built against the same local Clicky UI source (honouring `TELLTALE_CLICKY_UI_DIR`):

```sh
task install:symlinked
```

Production builds and normal frontend tests use published Clicky UI packages from the lockfile. A plain source checkout compiles with an embed sentinel but cannot serve the production UI until `make build` has generated it.

## Checks and build output

```sh
make lint
make test
make clean
```

`make test` builds first, runs Ginkgo through Gavel, and runs Vitest unit and live API tests. The Go binary is written to `.bin/telltale`; frontend bundles go to `webapp/dist`. Both are ignored. `make binary` compiles existing assets without Node or pnpm. `VERSION=v0.0.1 make build` sets the binary version explicitly.

Vite 8 uses Rolldown for bundling. The app keeps the React runtime in a shared chunk, loads backend status through a dynamic import, and retains automatic dependency and CSS splitting. HTML is revalidated; hashed assets use immutable caching.

## Releases

After main passes CI, a serialized release job checks out the exact verified source SHA, builds assets using locked published dependencies, and validates the release candidate. It creates one detached commit containing only `webapp/dist`, tags that commit, verifies the tagged Go build, and pushes only the tag. Main receives no generated asset commits.

```text
main:        A --- B --- C
                   \
                    assets commit <- v0.0.1
```

Versions use the same all-tags `svu patch` convention as Gavel, starting at `v0.0.1`. A retry reuses an existing tag only when its parent matches the verified source and its diff contains only assets. Tags are never force-moved. Release preparation is covered by disposable Git-repository tests and is not a command to run in a developer checkout.

Tagged source includes all embedded files, so installation needs only Go:

```sh
go install github.com/flanksource/telltale/cmd/telltale@v0.0.1
```

No release has been created by repository initialization. The example tag becomes usable only after the first successful release job.
