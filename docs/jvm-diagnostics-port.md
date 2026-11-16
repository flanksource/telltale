# JVM diagnostics

Telltale includes the generic diagnostic services ported from `oipa-cli` revision `c9e0ce0099d335543d9ef28bde5b9b111fe3842c`, under `com.flanksource.telltale.diag`. JMX, threads, metrics, logging, class browsing/decompilation, method instrumentation, compilation, redefinition and restoration use those implementations. Application settings, Spring/OIPA operations and rule-location interpretation belong in extensions.

## Build and install

`make build` creates the embedded agent at `pkg/agentinstall/java/assets/telltale-agent.jar` and three artifacts in `sdk/java/build/libs`: `telltale-diag.war`, `telltale-extension-api.jar` and `telltale-example-extension.jar`. Runtime classes target Java 8. CI tests Java 8, 11, 17, 21 and 25. The WAR uses `javax.servlet` and requires Servlet 3.1+ (for example, Tomcat 9).

Substitute a disposable application's PID and absolute target-host directories:

```bash
telltale agents install java --pid 1234 --directory /home/example/telltale/agent --service orders --allow-mutation --extensions-dir /home/example/telltale/extensions
telltale jvm catalog --agent 1234
telltale jvm jmx search --agent 1234 --pattern 'java.lang:*'
telltale jvm loaders --agent 1234
```

The installer also supports Kubernetes and SSH; `--hub-url` and `--token-file` connect the remote agent to Telltale. Local agents use PID identities; hub IDs come from `/api/agents`. Read-only inspection works by default. Writes, invocations, scripts, logging/bytecode changes and extension changes require both installation-time and per-call `--allow-mutation`. Operations affect the whole JVM, including other applications sharing a container.

## Operations and captures

`GET /api/agents/{agent}/catalog` returns live operation names, schemas and mutation flags. `POST /api/agents/{agent}/call` accepts `operation`, `params` and `allowMutation`. Agent identity comes from the route. Existing API authentication and cross-origin protection apply. The Agents page links to `/jvm/{agent}` for schema forms, results, scripts, extension uploads and compile previews. Class, loader, method and search selections persist in the URL; executable source does not.

Dedicated pages at `/jvm/{agent}/instrument`, `/jvm/{agent}/traces` and `/jvm/{agent}/notifications` use Clicky UI workspace panes, tables and live event feeds. Instrument accepts up to eight class/method targets, concrete loader identities, watch/trace options, event limits and a capture duration. Method selectors accept a name, wildcard or a name with its JVM descriptor, such as `compute(J)D`. Class and MBean links seed the corresponding form. Capture IDs and catalog filters persist in the URL; reloading the page reconnects to the selected server-owned recording. Stop ends the recording and restores instrumented bytecode; leaving a page does not stop it.

Traces keeps an agent-specific catalog beside runtime probe inspection. Notifications keeps the recording form, durable history, event stream and subscriber queue/drop counts together. The Records tab reuses the existing query browser, including the method call tree for JVM traces. Run responses and saved manifests carry each capture's target. Selector-based captures and older recordings without a target remain available through All runs instead of being attributed to a JVM by guesswork.

Operation families include `jvm.jmx.*`, `jvm.metrics`, `jvm.threads*`, `jvm.logging.*`, `jvm.classes*`, `jvm.loaders`, `jvm.probes.*`, `jvm.ognl.eval`, `jvm.groovy.eval`, `jvm.heap.histogram` and `jvm.extensions.*`. `jvm.heap.histogram` (`telltale jvm heap histogram`) returns the heap's classes by retained bytes from `GC.class_histogram`: `top` rows (default 100), `classPattern` as a glob over class names, and `live` to count only reachable objects, which forces a full GC first.

Every built-in operation is a typed command, grouped by task:

| Command | Serves |
| --- | --- |
| `jvm decompile`, `jvm decompile bytecode` | `jvm.classes.source`, `jvm.classes.bytecode` |
| `jvm patch compile` / `apply` / `lease` / `restore` / `list` | `jvm.classes.compile` / `patch` / `lease` / `restore` / `patches` |
| `jvm watch` | a `jvm_trace` capture of arguments, return values and exceptions, without nested calls |
| `jvm trace` | a `jvm_trace` capture with nested calls and timings |
| `jvm trace instrument` / `probes` / `read` / `freeze` | `jvm.classes.instrument`, `jvm.probes.list` / `read` / `freeze` |
| `jvm profiler` | a `jvm_jfr` capture on the agent seam |
| `jvm classes`, `jvm loaders`, `jvm jmx …`, `jvm threads …`, `jvm heap histogram`, `jvm metrics`, `jvm logging …`, `jvm ognl eval`, `jvm groovy eval`, `jvm extensions …`, `jvm notifications list`, `jvm status`, `jvm catalog` | the operation of the same name |

```bash
telltale jvm decompile --agent 1234 --class example.Worker
telltale jvm patch compile --agent 1234 --class example.Worker --source-file Worker.java
telltale jvm patch apply --agent 1234 --allow-mutation --class example.Worker --source-file Worker.java --lease-seconds 300
telltale jvm patch list --agent 1234
telltale jvm patch restore --agent 1234 --allow-mutation --class example.Worker
telltale jvm watch --agent 1234 --allow-mutation --duration 10s --targets '[{"class":"example.Worker","method":"compute"}]'
telltale jvm trace --agent 1234 --allow-mutation --duration 10s --targets '[{"class":"example.Worker","method":"compute"}]'
telltale jvm profiler --agent 1234 --engine async-profiler --duration 30s
telltale jvm heap histogram --agent 1234 --top 20 --class-pattern 'com.example.*'
```

Every operation's result has a typed model in `pkg/tracing/jvm` and its subpackages (`jvmruntime`, `classes`, `jmx`, `agentops`). In a terminal, a status or an acknowledgement prints on one line, such as `mutations allowed · instrumentation on · 0 patches` or `✓ logger global → FINE`. Lists print as a table under a one-line heading. Decompiled source is syntax-highlighted, and thread dumps and stacks print one frame per line. `--format json` (or yaml, csv and the others) prints the same model as structured data. An operation without a model, such as an extension's through `jvm call`, prints its JSON indented. A result that reports a failure, such as a compile with errors, a patch that was not applied or an instrument that armed no method, prints its diagnostics and the command exits non-zero. A test fails when a built-in operation in the catalog has no model.

`watch`, `trace` and `profiler` are the `jvm_trace` and `jvm_jfr` kinds with preset defaults. They record under the base kind, so the existing views read them. They take every flag of `telltale run jvm_trace|jvm_jfr`, which remain the generic forms, and their flags override the presets (`jvm trace --calls=false`). OpenAPI documents their typed inputs.

With `--follow`, the method events are folded into calls and printed one line per call when it exits: the time, the thread, the call with its arguments, the return value or exception, and the duration. `jvm trace` prints the calls nested inside each call as a tree under its line. When the run ends, a line per stream totals the calls, how many threw, the slowest, and any events the agent dropped. The run summary follows, with one line per capture and the `telltale records` command that reads it back. `telltale trace replay <run-id>` prints a recorded run the same way, and `--format ndjson` keeps the raw events.

```text
02:16:16.761 main Idle.work(103328) → 464967 1.4ms
02:16:16.761 main ├─ Idle.step 1.2ms
02:16:16.762 main └─ Idle.step 138µs
jvm_trace · 5 calls · slowest Idle.work 1.4ms
Run host20261116T001616Z · 1.1s
  jvm_trace · agent 88158 · 35 records · telltale records jvm_trace --run host20261116T001616Z
```

Over HTTP, start the same capture with `POST /api/traces`, which records into the server's own data dir. Each capture goes under `traces` by kind, with `"enabled": true` (or an array of such objects to arm a kind more than once, each element its own capture); on the agent seam no `pid` or command is needed. For example: `{"duration":"10s","traces":{"jvm_trace":{"enabled":true,"target":{"seam":"agent","agent":"1234","allowMutation":true},"targets":[{"class":"example.Worker","method":"compute"}],"calls":true}}}`.

Each parameter is a flag (`objectName` is `--object-name`). Open-ended values such as `jvm.jmx.write`'s `--value` or `jvm.jmx.invoke`'s `--args` take JSON. An empty `--level` for `jvm.logging.set` sends `null`, which inherits the parent's level. Source parameters also accept `--source-file`, and `jvm.extensions.install`/`replace` take `--jar-file`. These file flags read the local filesystem, so a request that arrives over HTTP must send the content inline. Mutating operations add `--allow-mutation`. The same commands appear in `/api/openapi.json` under `/api/v1/jvm/…` with typed inputs. `jvm call --operation <name> --params '{…}'` remains for operations only the live catalog knows, such as extension operations.

The commands come from the definitions. Each operation declares its command in `DiagnosticSchemas`, and each kind preset is a `Commands` entry on the kind's `traces.Registration`. Go reads operations from `internal/jvm/catalog.json`, a snapshot of the Java registry's `Diagnostics.catalog()`. After changing an operation, its command or its schema in `Diagnostics`/`DiagnosticSchemas`, regenerate the snapshot with `task generate`, or with `./sdk/java/gradlew -p sdk/java jvmCatalog`. `CatalogSnapshotTest` fails while the snapshot is stale. `jvm.classes.source` returns source, descriptors and line mappings. `jvm.classes.bytecode` returns base64 class bytes. `jvm.classes.compile` previews compilation without applying bytecode.

Instrumented classes are armed by retransformation through a retransform-capable transformer, so a later retransformation of the class by another agent (an APM agent, Arthas) or by a bytecode read re-applies every probe still armed on it instead of dropping them.

Captures share the existing run API, definitions, checkpoints and record stores:

| Kind | Purpose | Parameters |
| --- | --- | --- |
| `jvm_jfr` | JFR or async-profiler | Existing engine/event/settings options |
| `jvm_trace` | Watch values or trace nested calls | One to eight `targets` with `class`, `method`, optional `loader`; `args`, `return`, `exception`, `calls`, `platform`, `events`, `expand`, `arg-chars`, `gc`, `alloc` |
| `jvm_redefine` | Replacement source for a capture | `class`, optional `method`/`loader`, `source`, optional `classpath` |
| `jvm_notifications` | JMX notifications and decoded GC | `mbean` name/pattern, optional `types` prefixes and `capacity` (default 500, maximum 50,000) |
| `jvm_ognl` | Periodic expression samples | `expression`, optional `loader`, `intervalMs` (default 1,000, minimum 100) |

`jvm_redefine` requires a verifiable local `CodeSource` class artifact matching the selected runtime logical method after Telltale hooks are excluded, or an exact previously coordinated Telltale source body. Unverifiable origins and uncoordinated foreign bytecode changes to that method are rejected before mutation, with the class and descriptor in the error; opaque advice stays intact. Stopping a source capture preserves generic method traces, and stopping a trace preserves source edits. Manual class restoration and lease expiry still clear all coordinated edits and method probes for that class.

```bash
telltale jvm trace --agent 1234 --allow-mutation --duration 10s --targets '[{"class":"example.Worker","method":"compute"}]'
telltale run jvm_notifications --agent 1234 --duration 10s --mbean 'java.lang:type=GarbageCollector,*'
telltale run jvm_ognl --agent 1234 --allow-mutation --duration 5s --expression '@java.lang.Runtime@getRuntime().freeMemory()'
```

Method captures stream events and restore bytecode on stop, deadline, disconnect or event-budget completion. They renew a 120-second JVM-owned lease every 30 seconds; the JVM reaper restores abandoned leased patches. Direct patches expose lease controls through `jvm.classes.lease` and restore through `jvm.classes.restore`; inspect them with `jvm.classes.patches`. Ambiguous write targets require a real loader hash; grouped loaders are browsing filters. Dropped events remain visible in stored records.

## One-shot Groovy

```bash
telltale jvm groovy eval --agent 1234 --allow-mutation --source 'println args.name; return 3 + 4' --args '{"name":"example"}'
telltale jvm groovy eval --agent 1234 --allow-mutation --source-file sdk/java/examples/inspect.groovy --args '{"name":"orders"}' --timeout-seconds 30
```

`--loader` selects an application loader hash from `jvm.loaders`; omitting it selects the system loader. Bindings are `args` (JSON object), `application` (selected loader), `jmx`, `extensions`, `out` and `err`. Every execution isolates and closes its Groovy loader. Scalars return as `value`; other results use bounded object graphs. Standard output/error each have a 1 MiB limit. Errors propagate to the caller. Deadlines and cancellation are cooperative and cannot forcibly stop blocking application methods. Scripts and extensions are trusted operator code, not a sandbox.

## Hot extensions

```bash
telltale jvm extensions install --jar-file sdk/java/build/libs/telltale-example-extension.jar --agent 1234 --allow-mutation
telltale jvm call --agent 1234 --operation extension.example.greet --params '{"name":"operator"}'
telltale jvm extensions replace --jar-file sdk/java/build/libs/telltale-example-extension.jar --agent 1234 --allow-mutation
telltale jvm extensions list --agent 1234
telltale jvm extensions unload --id example --agent 1234 --allow-mutation
```

Each JAR has exactly one `DiagnosticExtension` provider listed in `META-INF/services/com.flanksource.telltale.DiagnosticExtension`. Compile against `telltale-extension-api.jar`; runnable source and a service descriptor live in `sdk/java/examples`. API version 1 defines ID, version, startup and cleanup. `OperationSpec` supplies operation schemas, title, description, mutation flag and handler. Names must start with `extension.<id>.`. Handlers return JSON-compatible JDK values. Child-first loaders isolate bundled dependencies while retaining one shared SPI identity.

`ExtensionContext.bindings` supplies application-loader-specific Groovy values under `extensions.<id>`. `mbeans()` exposes the MBean server. Register owned listeners/executors/resources using `own(AutoCloseable)`; unload closes them in reverse order. Failed cleanup remains visible as a failed extension and disables its operations. Cleanup must support retry after partial failure.

Installation verifies SHA-256 and stores immutable digest-named JARs under the absolute `extensionsDir`. An atomic `active.properties` manifest restores activation after JVM restart; cached JARs remain after unload. Uploads are limited to 16 MiB. Replace/unload refuse during active operation calls or Groovy executions. Catalog refresh exposes changes without restarting the agent, WAR or Telltale server.

## Standalone WAR

A `Deployer` (below) hot-deploys the WAR with its configuration embedded as `WEB-INF/telltale-diag.properties`. To deploy it by hand instead, configure an absolute properties file using the servlet/context `configFile` parameter or `-Dtelltale.diag.config=/etc/telltale/diagnostics.properties`; an embedded and an external configuration together are an error. Start from `sdk/java/examples/diagnostics.properties`:

- `issuer` and `audience`: the `iss` and `aud` every token must carry. The audience is the target id.
- `jwks` (inline JWKS JSON) or `jwksFile` (an absolute path), and/or `jwksUrl`: the issuer's public keys. On an unknown `kid` the WAR re-reads `jwksUrl`, at most once every 30 seconds, so a key rotation needs no redeploy.
- `runtimeDir` stores the extracted runtime, `extensionsDir` enables extensions and `allowMutation=true` allows writes.
- `configDigest` is echoed by `/health` so a deployer can tell whether the WAR it finds has its provision.

There is no shared token: the WAR holds only public keys, and `tokenFile` fails initialization. Unknown keys fail initialization too.

Every execution request carries `X-Telltale-Authorization: Bearer <jwt>`. The Kubernetes apiserver deletes `Authorization` from every request it proxies (`pods/proxy`, `services/proxy`), so the token never relies on it, and it is never a query parameter. A direct connection may send `Authorization` instead; both present with different values is a 400. The token is an RS256 JWT with `kid`, `iss`, `aud`, `sub`, `exp` (60 seconds leeway) and a space-separated `scope`. RS256 is used because the agent runs on Java 8, which has no Ed25519. The WAR verifies each call and fails closed: a missing, malformed, wrongly signed, wrong-audience or expired token is 401, and a token without the route's scope is 403. Scopes are one table in the agent. Every catalog operation and `/hello` probe names its scope, and minters ask for exactly that:

| Scope | Grants |
| --- | --- |
| `jvm:read` | `/hello`, `/catalog` and every read-only operation |
| `jvm:trace` | `jvm_trace`, `jvm_jfr` and `jvm_notifications` captures, `jvm.probes.freeze` |
| `jvm:instrument` | `jvm_redefine`, `jvm.classes.patch/instrument/restore/lease`, extension install/replace/unload |
| `jvm:eval` | `jvm.groovy.eval`, `jvm.ognl.eval`, `jvm_ognl` captures |
| `jvm:write` | `jvm.jmx.write/invoke`, `jvm.logging.set`, mutating extension operations |

Routes:

- `GET /health` needs no token. It answers `{status, apiVersion, configDigest}`.
- `GET /hello` gives the agent API, probes and catalog. `GET /catalog` gives the catalog alone.
- `POST /call` takes `{operation, params, allowMutation}`.
- `POST /captures` takes `{kind, params, allowMutation}` and answers `{id, streaming, contentType, fileName}`.
- `POST /captures/{id}` reads the capture so far and sets `X-Telltale-Complete`. `POST /captures/{id}/renew` extends its lease (204). `DELETE /captures/{id}` finishes it and returns the last reading.

Capture IDs belong to the WAR deployment; unloading it closes its captures, while agent captures stay independent.

The agent API version is `1`. A WAR that finds an older runtime already loaded in the JVM stays deployed, but answers `/health` with 503 `outdated` and refuses every other route. Restart the JVM to replace the runtime.

The first adapter creates the JVM-wide `com.flanksource.telltale:type=Diagnostics` MBean in a loader outside the servlet application. Other adapters share it through JDK types. Mutation/extension-directory configuration must agree. Original bytes, probe buffers, leases and extensions survive servlet redeployment. Restart the JVM to replace this shared runtime version. Instrumentation attaches lazily when required: use a full JDK in `java.home`/`JAVA_HOME`, or `-Dtelltale.diag.jdk-home` to select a matching JDK. Attach restrictions are reported as errors.

## Deployers

A `Deployer` (`crds/telltale.flanksource.com_deployers.yaml`, Go package `pkg/deploy`) puts the agent into the JVMs a selector picks and owns their auth. Executions never install anything. They reach a deployed target with a short-lived token the deployer's issuer minted. `examples/deployers/orders.yaml` shows both kinds.

- **`spec.selector`** has `kind`, `namespace`, `name` or `labels`, and `container`.
  - `kind` is `Deployment`, `StatefulSet` or `Pod`. `labels` match the workloads of that kind, and the targets are the running pods the workloads' own pod selectors match.
  - `name` selects the one workload (or pod) of that kind instead, which is what the web wizard's workload picker sends. A Helm-managed Deployment often carries only release labels, so naming it is the way to reach exactly its pods. A selector with both `name` and `labels` is refused.
  - `container` is the JVM container. A selector that matches no running pods, or names a container a pod lacks, is an error.
  - Each target is named `deployer/<deployer>/<namespace>/<pod>`. That name is the audience of its tokens.
- **`spec.javaContainer`** is keyed by `kind`, and each kind is a registry entry (`deploy.RegisterJavaContainer`) that validates its own fields.
  - **`tomcat`** embeds commons-db `HTTPConnection` as the Tomcat manager login: `connection`, `username`/`password`, `bearer`, `oauth` and `tls`.
    - `url` is a Go template rendered per target to the Tomcat base URL. It binds `.Pod.Name`, `.Pod.Namespace`, `.Pod.Labels`, `.Pod.IP`, `.Container` and `.Port "<name>"`. A binding the target lacks is an error, never `<no value>`.
    - The rendered URL goes through commons-db `ResolveConnectionURL` (`proxy://`, `portforward://`, `svc://`, `http(s)://`).
    - `url` is what the deploy uses: the manager upload and the `/health` probe. The deployer uploads a per-target `telltale-diag.war` to `<url><managerPath>/text/deploy?path=<contextPath>&update=true`. `managerPath` defaults to `/manager` and `contextPath` to `/telltale-diag`; `runtimeDir` is required.
    - The manager login needs its `Authorization` header, which the apiserver deletes from proxied requests. So `url` is a `portforward://<pod>.<namespace>:<port>?kind=pod` or a direct `http(s)://` URL. A `proxy://` URL, or a literal apiserver `.../pods|services/<name>/proxy` URL, is refused when the deployer is decoded.
    - `executionUrl` (optional, default `url`) is the base executions use, with `contextPath` appended ahead of any query. It may be `proxy://<pod>.<namespace>:<port>?kind=pod`, because the token travels in `X-Telltale-Authorization`. Executions use the connection's TLS and no manager login. The commons-db connection transport adds the apiserver's own credentials to proxy URLs.
  - **`attach`** copies the agent jar with `kubectl exec` and attaches it to `pid`. It starts the agent's HTTP listener on `bind`:`port`, which serves the same routes and verification as the WAR. `url` is the listener's URL template.
  - **`javaagent`** is registered but not implemented yet. A deployer naming it fails with that message.
- **`spec.auth.issuer`** names the connection whose `signingKey` named secret (an RSA PEM key) signs tokens. Its reference is the tokens' `iss`.
  - Agents are provisioned with the public JWKS only.
  - `spec.auth.jwksUrl` names where the issuer runtime serves its JWKS (`auth.Issuer` is an `http.Handler`), so a rotated key is picked up on its first unknown `kid`.

Deploying is idempotent. The deployer reads each target's `/health`, and a target already running agent API `1` with the same provision digest is left alone. A different provision is redeployed with `tomcat`. With `attach` it is an error, because an attached agent cannot be replaced without a JVM restart.

Each target reports `{target, namespace, pod, container, state, agentAPIVersion, endpoint, error}`, where `state` is `deployed`, `outdated` or `failed`. `outdated` means the JVM still runs another agent API after the deploy; restart the JVM to replace it.

`deploy.Connect` dials each deployed target over direct HTTP (`agentclient.DialHTTP`) into an agent registry. The agent ID is the target id, and its resource carries `telltale.deployer`, `telltale.target.id`, `k8s.namespace.name`, `k8s.pod.name` and `k8s.container.name`. Captures then name the target (`target.agent: <target id>`) or select it (`target.selector: {telltale.deployer: orders}`), never a URL.

Arm, checkpoint, renew, disarm and calls run through the same `agentclient.Agent` and `seam.AgentSeam` as hub agents:

- Each request carries a token for the scope the agent's `/hello` names.
- Streaming captures are read every 250 ms and renewed every 30 seconds.
- A capture ends complete when the agent sets `X-Telltale-Complete`.

### From the CLI

`telltale jvm deploy -f <deployer.yaml>` deploys with your kubeconfig (`--kubeconfig` to choose files) and the connections telltale reads, which must define the `spec.auth.issuer` connection and its `signingKey`. It prints each target's state and keeps the deployer with that status in the global config dir (`deployers/<name>.yaml`), even when some targets fail, so the deployed ones stay reachable. Run it again after pods are replaced; it is idempotent. `telltale jvm deployers` lists the kept targets (`--format json` prints them as JSON records). `--deployer '<yaml or json>'` passes the document itself instead of `-f`; exactly one of the two is required. `--dry-run` resolves the selector and prints each pod it picks with state `selected`, without an issuer, without installing anything and without keeping the deployer.

Over the API, `telltale serve` exposes the same command as `POST /api/v1/jvm/deploy`, executed on the server with its kubeconfig and connections. Send the document as `deployer`; `file` is refused over HTTP because it would read the server's filesystem. For example, `{"deployer":"apiVersion: telltale.flanksource.com/v1alpha1\nkind: Deployer\n...","dry-run":true}` answers the selected targets as `[{"target":"deployer/orders/shop/orders-0","namespace":"shop","pod":"orders-0","container":"app","state":"selected"}]`. Without `dry-run` it deploys and keeps the deployer as the CLI does, and `/api/v1/jvm/deployers` lists the kept targets as the same records.

Every `jvm` command and capture then names a target by its id, or by a short name. `--agent` takes the agent's id, its service, the last part of its id, its pid, a deployed target's deployer or pod, or a unique prefix of any of these. An exact name wins over prefixes. Leave `--agent` out when only one agent can serve the command: the one connected Java agent, the one connected agent offering the capture's probe, or the one deployed target. A name that matches several agents, or an omitted one with several to choose from, is refused with the candidates listed. The web wizard likewise arms a new agent capture on the only agent that can run it.

```bash
telltale jvm metrics --agent checkout        # the deployer's one target
telltale jvm metrics --agent checkout-7d     # a pod-name prefix
telltale jvm metrics                         # the only Java agent there is
```

A deploy confirms each target's health for up to 30 seconds and resolves the target's URL again on every attempt. A port-forward opened before the attached listener came up is torn down by its first refused connection, and resolving opens a new one, so a first deploy reports `deployed` once the agent answers.

By id:

```bash
telltale jvm deploy -f examples/deployers/orders.yaml
telltale jvm deployers
telltale jvm metrics --agent deployer/orders-batch/orders/orders-batch-7d9f-abcde
telltale jvm watch --agent deployer/orders-batch/orders/orders-batch-7d9f-abcde --allow-mutation --duration 10s --targets '[{"class":"example.Worker","method":"compute"}]'
```

The first use of a target id in a process loads its deployer, builds the issuer from the connection, resolves the target's endpoint (opening its port-forward) and connects. Each request then carries a token minted for that target, the scope of the operation or capture, and the subject `<user>@<host>`. Only `deployer/…` ids reach a cluster, so local agent lookups never depend on it. A server started with `telltale serve` resolves the same ids for `/api/v1/jvm/…` and `POST /api/traces`.

## Verification

For an interactive local demo, first run `make build`, then:

```bash
gavel fixtures examples/jvm-diagnostics.fixture.yaml --ui --addr 127.0.0.1
```

The fixture builds a disposable Spring application with embedded Tomcat, H2 and Artemis using the pinned dependencies in Gradle's `jvmDemo` task. It starts a local Telltale OTLP receiver, warms the application's clients and existing OpenTelemetry exporter, then attaches Telltale without restarting the JVM. A full JDK 17+ (`java`, `javac`, `jcmd`), Go, curl and the built `.bin/telltale` must be available. Use a Gavel build that includes `kind: telltale` and form steps. All listeners bind to loopback on allocated ports; no external application or database is required.

Native capture steps retain the Groovy, method tracing, compile preview, managed redefinition, OGNL and original-byte restoration checks. Protocol checks exercise incoming Spring MVC requests, outgoing RestTemplate/WebClient calls, JDBC/R2DBC statements and Spring JMS sending/listening. Deterministic held operations verify successive HTTP/SQL Top snapshots retain their operation IDs while elapsed time increases. Releasing the operations verifies the schema-v2 Profiles control sample clears the Top rows. Mirrored spans are compared with normal OTLP exporter delivery.

After verification, periodic traffic keeps the captures active. The final Exit form shows the diagnostics, capture and workload URLs and waits while you explore. Submit Exit or cancel to stop the owned captures, JVM and server. Captures, native snapshot evidence and agent artifacts remain in the unique `.tmp/jvm-demo/run.*` directory. Without `--ui`, run from a terminal to answer the same final form there. To run the same checks and clean up immediately, use:

```bash
gavel fixtures examples/jvm-diagnostics.fixture.yaml --param interactive=false
```

Java tests cover the copied services, real extension JAR lifecycle, isolated Groovy and embedded Tomcat WAR reload. The disposable-JVM installer integration checks packaged calls, trace budget completion, deadlines, method replacement/restoration and JFR decoding. API specs check live catalog changes, script integer precision and extension install/replace/unload over HTTP. UI tests check mutation consent and result conversion. Work is tracked by Gavel TODO `821b9a83-f816-4e6b-8b3d-c96ce5751f57`.
