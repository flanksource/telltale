# Trace kinds

Each trace kind records a particular source. Run one with `telltale run <kind>`, or combine captures using `telltale trace run --trace`. Run `telltale run <kind> --help` for the complete, schema-derived flags for your installed version.

The target seam determines what is observed: `process` follows a command or PID, `host` observes host resources or remote services, `connection` uses a configured database, `proxy` routes a command through a local proxy, `agent` observes an instrumented agent, and `self` observes Telltale's own instrumented sources. A self capture does not automatically record the command's activity.

| Kind | Records |
| --- | --- |
| [exec](/traces/exec) | Instrumented command executions |
| [goprof](/traces/goprof) | Go profiles at checkpoints |
| [http](/traces/http) | HTTP exchanges |
| [jvm_jfr](/traces/jvm_jfr) | JVM profiling samples |
| [llm](/traces/llm) | Instrumented LLM provider calls |
| [logs](/traces/logs) | Application logs |
| [process](/traces/process) | Process startup metadata, resource deltas and numeric metrics |
| [prometheus](/traces/prometheus) | Metrics scrapes |
| [query](/traces/query) | Query profile or provider results |
| [spans](/traces/spans) | Application or imported spans |
| [sql](/traces/sql) | Instrumented or proxied SQL statements |
| [sql_deadlock](/traces/sql_deadlock) | SQL Server deadlocks |
| [sql_plancache](/traces/sql_plancache) | SQL Server plan cost deltas |
| [sql_xevent](/traces/sql_xevent) | SQL Server Extended Events |
| [stacks](/traces/stacks) | Process stacks at checkpoints |
| [terminal](/traces/terminal) | Terminal events supplied by a host |

Runs are stored in the data directory. Use `telltale trace list --kind <kind>` to find runs, and `--follow`, `--tui`, or `--ui` to inspect captures while recording. Some kinds require an instrumented application, a connection, or external tools; their pages explain the relevant prerequisites.
