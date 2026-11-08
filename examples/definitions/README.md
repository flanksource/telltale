# Application and build trace definitions

Each YAML file is a complete `TraceDefinition`. Pass its path to `telltale run --definition`, or copy it to `.telltale/traces/<metadata.name>.yaml` in your project to select it by name. Run build commands in the target project's directory and adjust the definition path to point to your copy.

| Runtime | Application | Build |
|---|---|---|
| Java | [java-app.yaml](java-app.yaml): Flight Recorder samples, thread identities, process resources and descriptors | [java-build.yaml](java-build.yaml): Gradle/Maven process tree and resource samples |
| Go | [go-app.yaml](go-app.yaml): gops goroutine dumps, heap profiles, process resources and descriptors | [go-build.yaml](go-build.yaml): compiler, linker and cgo process tree and resource samples |
| Node | [node-app.yaml](node-app.yaml): process tree, resources, open files and network sockets | [node-build.yaml](node-build.yaml): bundler, type checker and worker process tree and resource samples |

```sh
telltale definitions validate ./examples/definitions/java-app.yaml
telltale run --definition ./examples/definitions/java-app.yaml --pid 1234 --duration 1m
telltale run --definition ./examples/definitions/go-app.yaml --pid 1234 --duration 1m
telltale run --definition ./examples/definitions/node-app.yaml -- node server.js
telltale run --definition ./examples/definitions/java-build.yaml -- ./gradlew --no-daemon build
telltale run --definition ./examples/definitions/go-build.yaml -- go build ./...
telltale run --definition ./examples/definitions/node-build.yaml -- pnpm run build
```

Replace `1234` with the application's PID. Java profiling requires a compatible JDK's `jcmd` on `PATH`, Flight Recorder support and permission to attach to that JVM; see [JVM capture prerequisites](../../docs/sqlserver-jvm.md#local-jvm). The Go application must already run `github.com/google/gops/agent`, with its port file readable by Telltale. Both app definitions read checkpoints every 10 seconds; `--every` overrides this. The Node definition uses OS process observation and does not collect V8 heap or CPU profiles.

All six definitions collect process resources and descriptor facets using the host's process tools, including `lsof`. They restrict captured environment variables to the listed runtime settings. Builds also sample CPU, memory and disk IO every 250 milliseconds, and finish when the command exits. Sampling can miss processes that exit between observations. Use Gradle's `--no-daemon` so the build does not reuse a daemon outside the traced tree; likewise, detached workers from an earlier build are outside the captured descendants.

To inspect results, run `telltale trace list`, then `telltale serve` with the same data directory and open the recorded run in the web UI. See [checkout.yaml](checkout.yaml) for proxies, connected agents, metrics and queries, or [logs-and-queries.yaml](logs-and-queries.yaml) for a host-only run.
