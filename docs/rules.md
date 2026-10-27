# Resource monitoring rules

Rules evaluate live groups and run configured hooks when a CEL condition remains true. Start with observation and inspect the proposed targets:

```sh
make build
.bin/telltale rules validate --rules examples/rules.yaml
.bin/telltale rules preview --rules examples/rules.yaml
.bin/telltale serve --rules examples/rules.yaml
.bin/telltale rules status
.bin/telltale rules events --rule agent-cpu --limit 20
```

After reviewing the configuration and preview, enable hooks by starting the server with `--enforce-rules`:

```sh
.bin/telltale serve --rules examples/rules.yaml --enforce-rules
```

The rules loop runs every two seconds even when no browser is open. Configuration is loaded once from the explicit file; restart to load edits. Process classification continues to use the work directory's `.telltale/groups.yaml`. One enforcing instance per OS user holds an advisory lock. Observation instances never invoke hooks.

## Resource values and CEL

`match` is a CEL boolean condition. True triggers the configured stages after `for`; `cpu < 25` therefore triggers below 25 percent. Use `&&` and `||` for compound conditions.

| Binding | Meaning |
| --- | --- |
| `cores` | Consumed logical cores, including fractional cores |
| `cpu` | Percent of total host CPU capacity: `100 * cores / host.cores` |
| `rss` | Sum of resident memory in bytes |
| `mem` | Percent of host physical RAM occupied by that RSS |
| `host.cores` | Logical-core count as a double for division |
| `host.memoryTotalBytes` | Host physical memory in bytes |
| `group` | Stable key, name, kind, context, member processes and owning sessions |
| `now` | Snapshot timestamp |
| `incident` | ID, rule, group, stage and timing; available in target expressions |

On an eight-core host, two consumed cores mean `cores == 2.0` and `cpu == 25.0`. `cores / host.cores > 0.25` means more than 25 percent of capacity. The existing Top API retains its per-core CPU percentage convention. CEL ordering comparisons accept integer thresholds such as `cores > 1`; use matching numeric types for equality, such as `cpu == 25.0`.

CPU is unknown until it can be sampled. Unknown inputs remain CEL unknowns, including in member filters. A condition whose final result is unknown cannot advance a stage or establish recovery. Known parts of a compound CEL condition can still determine its result. Errors are reported with the rule and expression; they are never converted into false or empty targets.

Expressions are compiled and type-checked at startup. The environment supports CEL list filtering, `sortBy`, `reverse`, `slice(start, end)`, and `cel.bind(name, value, expression)` for reusing intermediate selections; guard list bounds before slicing. Each evaluation has a one-million-unit cost limit and a 50 ms deadline.

## Grouping and rule fields

| Field | Meaning |
| --- | --- |
| `version` | Required configuration version, currently `1` |
| `rules[].name` | Required unique rule name |
| `groupBy` | `tree`, `application`, `repo`, `worktree`, `session`, or `host` |
| `match` | Required CEL trigger, including metadata and resource conditions |
| `for` | Required duration for which the trigger must remain true; `0s` is immediate |
| `recover` | Optional CEL recovery condition; defaults to `!match` |
| `recoverFor` | Required continuous recovery duration |
| `steps[].wait` | Additional continuous matching time after the preceding stage completes |
| `steps[].hook` | Hook type, CEL targets, and type-specific parameters |

Tree groups include descendants. Application groups collect processes under their nearest application bundle. Repository groups combine linked worktrees through their Git common directory; worktree groups keep checkout roots separate. Session groups use the agent and full session ID. The host group combines every observed process, including those without repository or session attribution. Members are counted once within a group. Separate matching rules all apply.

An incident opens after `for`. Each stage runs once per incident. When `match` becomes false, queued stages stop and their wait timers reset. Recovery closes the incident after `recoverFor`; the explicit recovery condition supports hysteresis. Group disappearance closes it immediately. Missing observations and gaps longer than two sampling intervals break timing continuity. Later configured stages can run after an ordinary hook failure if matching continues; expression errors block that rule until they resolve.

## Hooks and target expressions

Process targets expose `key`, `pid`, `ppid`, `name`, `command`, `context`, `cores`, `cpu`, `rss`, and `mem`. `groupName` is the process catalog's tool name, `groupRoot` identifies its job's anchor process, and `groupPath` contains its group key and ancestor group keys. These fields let rules count jobs and select their descendants across nested tool groups. Session targets expose `key`, `agent`, `id`, verified anchor keys, `workspace`, `surface`, and attribution errors. Returned objects must be unchanged references from `group.members` or `group.sessions`. Targets are checked against the latest snapshot and live process identities before actions are sent. Empty selections are recorded as `no-targets`.

| Type | Parameters and behavior |
| --- | --- |
| `renice` | `nice: 0..19`; only lowers priority and preserves an already lower priority. Changes persist after recovery. |
| `signal` | `signal: SIGHUP`, `SIGINT`, `SIGTERM`, or `SIGKILL`; sends to selected process identities individually. SIGHUP's default action terminates a process. |
| `session-interrupt` | `via: captain` requires the owning server's `endpoint`; `via: cmux` requires an explicit `key`, such as `Escape` for Claude. |
| `exec` | `command: [executable, arg, ...]`; receives the full decision as JSON on stdin and records exit status, stdout, and stderr. |

Every hook accepts an optional positive `timeout`, defaulting to `10s`. Executable output is capped at 64 KiB per stream; cancellation stops its isolated process group. No automatic retries occur. Hook dispatch is serialized; identical built-in actions from one snapshot are coalesced and retain each incident's result.

Captain control checks `/api/captain/sessions/<id>/chat` for a running interruptible session, then calls its interrupt endpoint. cmux verifies the live anchor in the exact workspace/surface before sending the configured key. Missing or ambiguous attribution fails visibly. Process actions refuse stale identities, PID 1, the monitor, its ancestors, and other users' processes.

For a Captain-controlled session:

```yaml
type: session-interrupt
targets: group.sessions
via: captain
endpoint: http://localhost:8081
```

An executable hook can select either a homogeneous process list or a session list:

```yaml
type: exec
targets: group.members.filter(p, p.cores > 0.5)
command: [/usr/local/bin/resource-hook, --event, breach]
timeout: 5s
```

## Concurrent browser, test and build jobs

[The concurrency example](../examples/concurrency-rules.yaml) uses `groupBy: host` to detect excessive independent jobs across repositories:

| Rule | Allowed concurrent jobs | Counted roots |
| --- | --- | --- |
| `agent-browser-concurrency` | 4 | `agent-browser` catalog groups, including native platform binaries |
| `vitest-concurrency` | 2 | Vitest runner groups; workers remain members of their runner |
| `go-build-concurrency` | 2 combined | `go build`, `go install`, `go run`, and `go test` groups |

After 15 seconds above the limit, the rules renice excess job trees to 10. If the excess continues for another 30 seconds, they send SIGTERM to those job trees. The lowest root PIDs fill the allowed slots; higher root PIDs and their descendants are selected deterministically. The selectors are evaluated again for each stage. Each stage runs once per incident; these sampled policies do not prevent new jobs from starting. Change both the trigger threshold and the target slice boundary when adjusting a limit. These rules depend on the corresponding process catalog names; custom catalog overrides must preserve those names or update the expressions.

```sh
.bin/telltale rules validate --rules examples/concurrency-rules.yaml
.bin/telltale rules preview --rules examples/concurrency-rules.yaml
.bin/telltale serve --rules examples/concurrency-rules.yaml
```

The final command observes the concurrency rules. Add `--enforce-rules` to execute their hooks. To combine these policies with CPU/memory policies, put their entries under the same `rules` list in one configuration file.

## Rules and events in the UI

Open `/top?topTab=rules` to see configured conditions, incident states, escalation steps, and live target previews. Select a rule to inspect it, and expand a preview group to see the exact process identities and sessions selected by each step. The mode indicator distinguishes observation from enforcement.

Open `/top?topTab=events`, or use a rule's **View events** button, to inspect transitions, action intents and results. Filters cover rule, group, incident, timestamp and event limit. Inspecting an event shows targets, errors, exit status and captured output; the full payload is available on demand. Tabs, filters and selected events are preserved in the URL.

**Refresh interval** controls periodic UI requests: 1, 2, 5, 10, 30 or 60 seconds, or **Paused**. The default is one second and the choice is saved in this browser. **Refresh now** updates the active monitoring queries, including while paused. The setting applies to live groups, running group recordings, process views, rules and events; process facets retain a five-second minimum because they run lsof. Event filter edits are debounced for 250 ms. Background rule sampling and enforcement continue independently every two seconds.

## API and history

| GET route | Response |
| --- | --- |
| `/api/rules` | Effective configuration, revision, configuration schema and CEL binding descriptions |
| `/api/rules/preview` | Match results, resource values, target selections and errors |
| `/api/rules/status` | Mode, sample timestamp, runtime error and incident states |
| `/api/rules/events` | Durable transitions, intents and results; filter by `rule`, `group`, `incident`, `since`, and `limit` |

`since` accepts RFC3339; `limit` defaults to 100 and accepts 1..10000. CLI status/events use `--server`, defaulting to `http://localhost:8080`. HTTP routes are read-only and appear in `/api/openapi.json`.

History is stored through recordstore in `.telltale/monitor`. Action intents are written before dispatch. Persistence errors stop enforcement until restart. Restart restores stages for the current configuration and mode, requires fresh timing observations, and skips unfinished attempts whose outcome is unknown. Switching from observation to enforcement starts separate enforcement incident state. Budget rules react to sampled usage; they do not impose kernel CPU or memory quotas.
