# OTLP trace receiver

Approved scope: receive OTLP/HTTP and OTLP/gRPC continuously while enabled, persist received spans to local record stores, and expose them in the existing OpenTelemetry search and waterfall.

## Endpoints and configuration

- Mount `POST /v1/traces` on the application HTTP server. Accept OTLP protobuf and JSON, including gzip, and return the same encoding.
- Serve the standard gRPC TraceService Export method on a separate listener.
- Use Collector pdata/ptraceotlp v1.69.0 for codecs and gRPC registration.
- Add `serve --otlp` (disabled by default), `--otlp-grpc-listen localhost:4317`, `--otlp-retention 720h`, `--otlp-max-request-bytes 4194304`, `--otlp-max-spans 10000`, and `--otlp-max-inflight 8`.
- HTTP uses the existing server port; `serve --otlp --port 4318` provides the conventional OTLP HTTP port.
- Apply the existing API bearer-token policy independently to both listeners. Loopback allows unauthenticated access; non-loopback requires an API-scoped token. Protect `/v1/traces` explicitly outside `/api/`.

## Ingestion and storage

- Keep receiver logic in `internal/otelreceiver`; keep server and CLI wiring thin. Both transports share the same ingestion service and admission limit.
- Open a stable SQLite result store at `.telltale/otel/records.sqlite`, separate from command-run manifests, using commons-db recordstore/recordresults.
- Register indexed received-span projections and retain canonical OTLP protobuf for each span with its resource and instrumentation scope.
- Preserve typed attributes, events, links, status messages, schema URLs, flags, and dropped-item counts. Apply the existing secret-redaction policy to projections and retained native data before persistence.
- Acknowledge only after the accepted batch commits. Deduplicate persistently by trace/span ID; reject conflicting copies permanently.
- Reject malformed envelopes before writing. Commit valid spans in a mixed batch and return OTLP partial success for invalid spans. Return retryable errors for persistence failure and overload.
- Bound request bytes before and after decompression, span count, and concurrent work. Never truncate or silently drop spans.
- Bind listeners and open storage before readiness. Stop intake, drain requests, and close storage in order. Surface lifecycle and persistence errors.

## Query integration

- Add source `receiver`, named Received traces, to `/api/otel/sources` when enabled or a stored receiver database exists. Stored traces remain readable with ingestion disabled.
- Reuse current filters, complete-trace grouping, error highlighting, and the waterfall. Retrieve complete traces beyond search time bounds.
- Keep resource values typed in the query API and expose native OTLP metadata in the span inspector using shared Clicky UI components.

## Acceptance criteria

- Standard Go HTTP/protobuf and gRPC exporters can send spans without a Telltale agent or arming.
- HTTP JSON and gzip use standard OTLP identifiers and response encodings.
- Accepted spans survive restart and become searchable and inspectable in `/otel`.
- Retries do not duplicate rows; conflicting copies and invalid spans have explicit rejection responses.
- Native metadata and typed values survive persistence, except expected secret redaction.
- Limits, authentication, overload, storage failure, startup failure, and shutdown are covered by focused tests.
- Go Ginkgo tests separate pure conversion from SQLite integration. API integration tests, frontend tests, build, and live desktop/compact browser verification cover the final surfaces.
- Run `make lint` and report the known baseline Go violations separately without adding suppressions.

## Delivery

Document runnable exporter environment variables and Collector configuration for HTTP and gRPC. Receiver-management screens, OpenSearch forwarding, tenant routing, logs, and metrics ingestion are outside this change. Preserve the existing dirty working tree and do not commit.
