# JVM diagnostics

Telltale includes the generic diagnostic services ported from `oipa-cli` revision `c9e0ce0099d335543d9ef28bde5b9b111fe3842c`, under `com.flanksource.telltale.diag`. JMX, threads, metrics, logging, class browsing/decompilation, method instrumentation, compilation, redefinition and restoration use those implementations. Application settings, Spring/OIPA operations and rule-location interpretation belong in extensions.

## Build and install

`make build` creates the embedded agent at `pkg/agentinstall/java/assets/telltale-agent.jar` and three artifacts in `sdk/java/build/libs`: `telltale-diag.war`, `telltale-extension-api.jar` and `telltale-example-extension.jar`. Runtime classes target Java 8. CI tests Java 8, 11, 17 and 21. The WAR uses `javax.servlet` and requires Servlet 3.1+ (for example, Tomcat 9).

Substitute a disposable application's PID and absolute target-host directories:

```bash
telltale agents install java --pid 1234 --directory /home/example/telltale/agent --service orders --allow-mutation --extensions-dir /home/example/telltale/extensions
telltale jvm catalog --agent 1234
telltale jvm call --agent 1234 --operation jvm.jmx.search --params '{"pattern":"java.lang:*"}'
telltale jvm call --agent 1234 --operation jvm.loaders
```

The installer also supports Kubernetes and SSH; `--hub-url` and `--token-file` connect the remote agent to Telltale. Local agents use PID identities; hub IDs come from `/api/agents`. Read-only inspection works by default. Writes, invocations, scripts, logging/bytecode changes and extension changes require both installation-time and per-call `--allow-mutation`. Operations affect the whole JVM, including other applications sharing a container.

## Operations and captures

`GET /api/agents/{agent}/catalog` returns live operation names, schemas and mutation flags. `POST /api/agents/{agent}/call` accepts `operation`, `params` and `allowMutation`. Agent identity comes from the route. Existing API authentication and cross-origin protection apply. The Agents page links to `/jvm/{agent}` for schema forms, results, scripts, extension uploads and compile previews. Class, loader, method and search selections persist in the URL; executable source does not.

Dedicated pages at `/jvm/{agent}/instrument`, `/jvm/{agent}/traces` and `/jvm/{agent}/notifications` use Clicky UI workspace panes, tables and live event feeds. Instrument accepts up to eight class/method targets, concrete loader identities, watch/trace options, event limits and a capture duration. Method selectors accept a name, wildcard or a name with its JVM descriptor, such as `compute(J)D`. Class and MBean links seed the corresponding form. Capture IDs and catalog filters persist in the URL; reloading the page reconnects to the selected server-owned recording. Stop ends the recording and restores instrumented bytecode; leaving a page does not stop it.

Traces keeps an agent-specific catalog beside runtime probe inspection. Notifications keeps the recording form, durable history, event stream and subscriber queue/drop counts together. The Records tab reuses the existing query browser, including the method call tree for JVM traces. Run responses and saved manifests carry each capture's target. Selector-based captures and older recordings without a target remain available through All runs instead of being attributed to a JVM by guesswork.

Operation families include `jvm.jmx.*`, `jvm.metrics`, `jvm.threads*`, `jvm.logging.*`, `jvm.classes*`, `jvm.loaders`, `jvm.probes.*`, `jvm.ognl.eval`, `jvm.groovy.eval` and `jvm.extensions.*`. Use the live catalog for parameters. `jvm.classes.source` returns source, descriptors and line mappings. `jvm.classes.bytecode` returns base64 class bytes. `jvm.classes.compile` previews compilation without applying bytecode.

Captures share the existing run API, definitions, checkpoints and record stores:

| Kind | Purpose | Parameters |
| --- | --- | --- |
| `jvm_jfr` | JFR or async-profiler | Existing engine/event/settings options |
| `jvm_trace` | Watch values or trace nested calls | One to eight `targets` with `class`, `method`, optional `loader`; `args`, `return`, `exception`, `calls`, `platform`, `events`, `expand`, `arg-chars`, `gc`, `alloc` |
| `jvm_redefine` | Replacement source for a capture | `class`, optional `method`/`loader`, `source`, optional `classpath` |
| `jvm_notifications` | JMX notifications and decoded GC | `mbean` name/pattern, optional `types` prefixes and `capacity` (default 500, maximum 50,000) |
| `jvm_ognl` | Periodic expression samples | `expression`, optional `loader`, `intervalMs` (default 1,000, minimum 100) |

```bash
telltale run jvm_trace --agent 1234 --allow-mutation --duration 10s --targets '[{"class":"example.Worker","method":"compute"}]' --args --return --calls
telltale run jvm_notifications --agent 1234 --duration 10s --mbean 'java.lang:type=GarbageCollector,*'
telltale run jvm_ognl --agent 1234 --allow-mutation --duration 5s --expression '@java.lang.Runtime@getRuntime().freeMemory()'
```

Method captures stream events and restore bytecode on stop, deadline, disconnect or event-budget completion. They renew a 120-second JVM-owned lease every 30 seconds; the JVM reaper restores abandoned leased patches. Direct patches expose lease controls through `jvm.classes.lease` and restore through `jvm.classes.restore`; inspect them with `jvm.classes.patches`. Ambiguous write targets require a real loader hash; grouped loaders are browsing filters. Dropped events remain visible in stored records.

## One-shot Groovy

```bash
telltale jvm groovy --agent 1234 --allow-mutation --source 'println args.name; return 3 + 4' --args '{"name":"example"}'
telltale jvm groovy --agent 1234 --allow-mutation --file sdk/java/examples/inspect.groovy --args '{"name":"orders"}' --timeout 30
```

`--loader` selects an application loader hash from `jvm.loaders`; omitting it selects the system loader. Bindings are `args` (JSON object), `application` (selected loader), `jmx`, `extensions`, `out` and `err`. Every execution isolates and closes its Groovy loader. Scalars return as `value`; other results use bounded object graphs. Standard output/error each have a 1 MiB limit. Errors propagate to the caller. Deadlines and cancellation are cooperative and cannot forcibly stop blocking application methods. Scripts and extensions are trusted operator code, not a sandbox.

## Hot extensions

```bash
telltale jvm extensions install sdk/java/build/libs/telltale-example-extension.jar --agent 1234 --allow-mutation
telltale jvm call --agent 1234 --operation extension.example.greet --params '{"name":"operator"}'
telltale jvm extensions replace sdk/java/build/libs/telltale-example-extension.jar --agent 1234 --allow-mutation
telltale jvm extensions list --agent 1234
telltale jvm extensions unload example --agent 1234 --allow-mutation
```

Each JAR has exactly one `DiagnosticExtension` provider listed in `META-INF/services/com.flanksource.telltale.DiagnosticExtension`. Compile against `telltale-extension-api.jar`; runnable source and a service descriptor live in `sdk/java/examples`. API version 1 defines ID, version, startup and cleanup. `OperationSpec` supplies operation schemas, title, description, mutation flag and handler. Names must start with `extension.<id>.`. Handlers return JSON-compatible JDK values. Child-first loaders isolate bundled dependencies while retaining one shared SPI identity.

`ExtensionContext.bindings` supplies application-loader-specific Groovy values under `extensions.<id>`. `mbeans()` exposes the MBean server. Register owned listeners/executors/resources using `own(AutoCloseable)`; unload closes them in reverse order. Failed cleanup remains visible as a failed extension and disables its operations. Cleanup must support retry after partial failure.

Installation verifies SHA-256 and stores immutable digest-named JARs under the absolute `extensionsDir`. An atomic `active.properties` manifest restores activation after JVM restart; cached JARs remain after unload. Uploads are limited to 16 MiB. Replace/unload refuse during active operation calls or Groovy executions. Catalog refresh exposes changes without restarting the agent, WAR or Telltale server.

## Standalone WAR

Deploy `telltale-diag.war` and configure an absolute properties file using the servlet/context `configFile` parameter or `-Dtelltale.diag.config=/etc/telltale/diagnostics.properties`. Start from `sdk/java/examples/diagnostics.properties`. `tokenFile` holds a token of at least 16 characters, `runtimeDir` stores the extracted runtime, and `extensionsDir` enables extensions. Protect configuration/token files with filesystem permissions. Set `allowMutation=true` when required.

Routes are `GET /health`, authenticated `GET /catalog`, `POST /call`, `POST /captures`, `POST /captures/{id}` (checkpoint/download) and `DELETE /captures/{id}` (finish/download). All except health require `Authorization: Bearer <token>`. Capture creation accepts `kind`, `params` and `allowMutation`. IDs belong to the WAR deployment; unloading it closes its captures while agent captures remain independent.

The first adapter creates the JVM-wide `com.flanksource.telltale:type=Diagnostics` MBean in a loader outside the servlet application. Other adapters share it through JDK types. Mutation/extension-directory configuration must agree. Original bytes, probe buffers, leases and extensions survive servlet redeployment. Restart the JVM to replace this shared runtime version. Instrumentation attaches lazily when required: use a full JDK in `java.home`/`JAVA_HOME`, or `-Dtelltale.diag.jdk-home` to select a matching JDK. Attach restrictions are reported as errors.

## Verification

For an interactive local demo, first run `make build`, then:

```bash
gavel fixtures examples/jvm-diagnostics.fixture.yaml --ui --addr 127.0.0.1
```

The fixture compiles a disposable JVM, attaches the agent and starts the diagnostics UI on an allocated local port. Native Gavel `kind: telltale` steps check Groovy, method tracing, compile preview, managed redefinition, OGNL and original-byte restoration through HTTP. Use a Gavel build that includes this kind. The final form shows the UI URL and waits while you explore. Submit Exit or cancel to stop both supervised processes. Captures and agent artifacts remain in the unique `.tmp/jvm-demo/run.*` directory. A full JDK (`java`, `javac`, `jcmd`), curl and the built `.bin/telltale` must be available. Without `--ui`, run from a terminal to answer the same final form there.

Java tests cover the copied services, real extension JAR lifecycle, isolated Groovy and embedded Tomcat WAR reload. The disposable-JVM installer integration checks packaged calls, trace budget completion, deadlines, method replacement/restoration and JFR decoding. API specs check live catalog changes, script integer precision and extension install/replace/unload over HTTP. UI tests check mutation consent and result conversion. Work is tracked by Gavel TODO `821b9a83-f816-4e6b-8b3d-c96ce5751f57`.
