# Receive OpenTelemetry traces

Start Telltale with continuous trace ingestion enabled:

```bash
telltale serve --otlp --port 4318
```

This serves OTLP/HTTP at `http://localhost:4318/v1/traces`, OTLP/gRPC at `localhost:4317`, and the trace viewer at `http://localhost:4318/otel`. Choose **Received** as the source; the search runs as soon as it changes. No Telltale agent, capture run, or arming step is required.

## Trace Telltale itself

Enable HTTP self-tracing with a single flag:

```bash
telltale serve --self-trace
```

This automatically enables the receiver. Open `http://localhost:8080/otel?source=records&run=receiver&service=telltale` to inspect Telltale's own requests. Self traces carry `service.name=telltale` and `service.version` from the running binary, with HTTP method, matched route, status and duration. The middleware runs in development and production, preserves incoming W3C trace context, samples new root requests, and respects an unsampled parent.

Self-tracing sends OTLP over the actual bound localhost gRPC listener, including a port chosen with `--otlp-grpc-listen 127.0.0.1:0`. Ambient OTLP endpoint and proxy settings do not redirect it. This mode requires a loopback gRPC binding; the main HTTP listener keeps its existing bind and authentication options. Ordinary `serve` and `serve --otlp` do not instrument requests.

Request and response bodies, headers and query strings are not captured. OTLP ingestion requests are excluded, preventing export feedback. Other HTTP requests, including trace searches, produce spans. Existing receiver redaction applies before persistence.

Exports are batched every second, with a bounded queue of 2,048 spans and batches of up to 512. A full queue waits for capacity. Export failures are logged and returned during shutdown. Shutdown stops HTTP acceptance, drains requests, and gives self-tracing up to five seconds to flush while the gRPC receiver is still running; the receiver then drains and storage closes. Persisted self traces remain available after restarting without `--self-trace`.

## Configure an instrumented application

For an SDK configured from standard OpenTelemetry environment variables, use HTTP/protobuf:

```bash
export OTEL_SERVICE_NAME=checkout
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://localhost:4318/v1/traces
export OTEL_EXPORTER_OTLP_TRACES_COMPRESSION=gzip
```

Alternatively, use gRPC:

```bash
export OTEL_SERVICE_NAME=checkout
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_TRACES_COMPRESSION=gzip
```

Run the instrumented application after setting these variables. The traces-specific HTTP endpoint includes `/v1/traces`; a generic `OTEL_EXPORTER_OTLP_ENDPOINT` instead uses the base URL, and the SDK adds the signal path. SDKs with programmatic exporter configuration may require their own endpoint options.

From this source checkout, `go run ./examples/otel` exports a two-span demonstration trace using the selected environment configuration. It includes a child span with error status, typed attributes, and an event.

## Forward from an OpenTelemetry Collector

The runnable [Collector configuration](../examples/otel-collector.yaml) listens on ports 14317 and 14318 and forwards traces to Telltale over HTTP/protobuf. With Telltale already listening on 4318:

```bash
otelcol-contrib --config examples/otel-collector.yaml
```

Point your application's exporter at `http://localhost:14318/v1/traces` for HTTP or `http://localhost:14317` for gRPC. To forward from the Collector over gRPC instead, replace the pipeline's `otlphttp/telltale` exporter with the included `otlp/telltale` exporter.

## Server options

| Option | Default | Meaning |
| --- | --- | --- |
| `--otlp` | disabled | Enable both trace receivers for the lifetime of the server. |
| `--self-trace` | disabled | Trace Telltale HTTP requests to its local gRPC receiver; automatically enables `--otlp`. |
| `--port` | `8080` | Existing HTTP API/UI port, also used for `POST /v1/traces`. |
| `--otlp-grpc-listen` | `localhost:4317` | Separate gRPC bind address; port `0` selects an available port. |
| `--otlp-retention` | `720h` | Retain spans from ingestion time; old event timestamps are accepted. |
| `--otlp-max-request-bytes` | `4194304` | Maximum compressed and decompressed request size, up to 1 GiB. |
| `--otlp-max-spans` | `10000` | Maximum number of spans in one export. |
| `--otlp-max-inflight` | `8` | Shared admission limit across HTTP and gRPC exports. |
| `--data-dir` | `$XDG_CACHE_HOME/telltale`, else the OS cache dir | Directory holding telltale's trace data, including the receiver's store. |

HTTP accepts `application/x-protobuf` and standard OTLP `application/json`, with optional gzip. Responses use the request encoding. gRPC implements the standard TraceService/Export RPC and supports gzip. Logs and metrics exports are not supported.

## Authentication

Each listener follows the existing API bearer-token policy independently: loopback listeners allow local exporters; non-loopback listeners require an API-scoped token. An agent-scoped token cannot export traces. HTTP protects `/v1/traces` explicitly, including outside the `/api` prefix. Exporter headers can be configured with:

```bash
export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Bearer ${TELLTALE_API_TOKEN}"
```

Use the existing API-token management workflow to obtain `TELLTALE_API_TOKEN`. The listeners serve plaintext HTTP/gRPC; use a TLS ingress when receiving over an untrusted network.

## Read received spans as the `receiver` run

The receiver's store is a standing store, served wherever runs are read as the pseudo-run `receiver`. It is listed after the command runs, with status `running` while a `telltale serve --otlp` process holds `otel/receiver.lock` under the data dir and `standing` otherwise, so the newest run of a kind is still a command run when there is one. `--since` keeps it, since it is continuous.

```sh
telltale trace list --kind spans
telltale records spans --run receiver --filter service=checkout --limit 5
telltale records spans --run receiver --stats durationMs --group-by operation
telltale records spans --run receiver --follow
```

Over HTTP, `GET /api/traces` lists it, `GET /api/traces/receiver/traces/spans/records` reads its rows, and `GET /api/otel?source=records&run=receiver` searches it as traces; the `/otel` page offers it as the **Received** source. A recorded-trace search without a run covers command runs and the receiver together.

## Persistence and responses

Received spans are stored as the `spans` record kind, the same record SDK spans and OpenSearch imports use: `key` (trace ID/span ID), `traceId`, `spanId`, `parentId`, `service`, `operation`, `kind`, `startedAt`, `durationMs`, `status` (`ok`, `error` or `unset`), `attributes`, `resource`, `source` (`otlp`), and the redacted `native` request with its `digest`. They live in the shared SQLite record store under `otel/spans` in the data dir (`--data-dir`, by default `$XDG_CACHE_HOME/telltale`, else the OS cache dir such as `~/Library/Caches/telltale` on macOS or `~/.cache/telltale` on Linux), with logical database name `records.sqlite`; the shared backend owns the physical versioned layout (currently `v6/records.sqlite`), and the writer lock is `otel/receiver.lock`. Earlier releases kept the store under the working directory's `.telltale/otel`; that data, and spans stored as the older `otlp` kind, are not read or migrated: delete `./.telltale/otel` once it is no longer needed. One receiver process owns writes for a data dir. Traces remain searchable after restarting without `--otlp`.

The receiver acknowledges accepted spans after the batch commits. A retry with the same trace ID, span ID, and redacted content succeeds without another row. Conflicting content is rejected and the original remains. A batch containing invalid spans returns `partialSuccess.rejectedSpans` while committing its valid spans; malformed envelopes commit nothing. Storage failures and admission overload return retryable HTTP 503 / gRPC Unavailable. HTTP 503 includes `Retry-After`. Request and span limits return HTTP 413 / gRPC ResourceExhausted; exporters must reduce the batch. Retained per-span resource/scope contexts are also bounded to eight times the request-byte limit to prevent context amplification.

Native metadata includes resource and scope attributes, schema URLs, events, links, flags, status messages, and dropped-item counts. The existing secret-redaction policy runs before both projections and native payloads are stored. Attributes and resource values of received spans are read back from the native request, so typed values, 64-bit integers included, stay exact in the query API; native OTLP JSON preserves 64-bit integers as strings. The span inspector's **OpenTelemetry metadata** section allows expanding and copying the retained payload.

Both listeners and storage must initialize before startup reports the API URL. Shutdown drains HTTP requests, flushes self-tracing when enabled, and then stops and drains the receiver before closing storage. Retention uses the shared backend's expiration and compaction behavior; it does not delete the database when ingestion is disabled.
