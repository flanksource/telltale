# JVM Top

Monitor one Java runtime from a terminal or the web. JVM Top reads the agent's existing metrics and thread operations; install or deploy an agent first. With one discovered Java agent, no selector is needed. Otherwise, `--agent` accepts its ID, service, pod, PID, or another unique short name.

```sh
telltale jvm top
telltale jvm top --agent orders --every 2s
telltale jvm top --agent orders --server http://localhost:8080
telltale jvm top --agent orders --once --format json
```

`--every` sets the refresh delay after each completed sample (default 2s, minimum 1s). `--once` prints one snapshot and exits. Redirected output and structured formats also read one snapshot. `--server` samples through a running Telltale hub and uses `TELLTALE_API_TOKEN` for authenticated hubs.

The terminal uses the same compact meters, styles and Clicky table as `telltale top`. Use arrows or j/k to select a thread, s to cycle sorting, S to reverse it, / to filter, P or space to pause/resume, d or Enter to inspect the selected thread's stack, ? for help, and q to quit. Enter keeps a filter; Esc clears it. In stack inspection, arrows scroll the wrapped stack, Esc or d closes it while remaining paused, and P or space resumes live sampling. Threads that exit before inspection produce an explicit error.

Open `/jvm/{agent}/top`, or choose JVM diagnostics from the Agents page. Top is the default JVM view. Thread selection, filters and ordering persist in the URL. Polling pauses during inspection and while the page is hidden. A failed refresh leaves the last snapshot visibly marked stale.

Process CPU is a percentage of the JVM's available processor capacity; thread CPU is a percentage of one core, sampled over 200ms. Garbage collector counts and times are lifetime totals. Unavailable values remain unavailable; unbounded memory pools have no artificial maximum. This view does not enable CPU timing, force garbage collection, instrument methods or record history.

`POST /api/v1/jvm/top` accepts `agent`, `once` and `format: "json"` and always returns one snapshot through the usual Clicky command response. Its `output` string contains a frame with `agentId`, `sampledAt`, `metrics` and `threads`; the generated OpenAPI includes the `JVMTopFrame` schema. Sampling and stack reads share the existing API authentication rules.
