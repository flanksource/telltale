# Telltale HTTP self-tracing to localhost

Approved scope: add `serve --self-trace`, disabled by default. It automatically enables the local OTLP receiver and continuously records inbound HTTP requests as OpenTelemetry spans, visible under Received traces in `/otel` with `service.name=telltale`. No agent registration or arming is required.

## Implementation

- Add the CLI flag and server option. Reuse receiver settings, storage, retention and query APIs.
- Own an OpenTelemetry tracer provider and bounded batching exporter. Send to the receiver's actual loopback gRPC address, including dynamic ports, with explicit connection configuration independent of ambient OTLP endpoint variables.
- Require a loopback gRPC binding when self-tracing is enabled. Reject incompatible bindings before readiness. Preserve the HTTP listener's bind/auth behavior.
- Use the installed `otelhttp` middleware at the shared routing entrypoint for development and production. Pass the provider explicitly, preserve W3C trace context, use route-based names, and record status and duration. Exclude OTLP ingestion from instrumentation.
- Sample new root requests and respect the parent's sampling decision. Do not enable request/response body or header capture. Reuse receiver redaction.
- Stop HTTP acceptance and drain handlers, flush/shut down the tracer while gRPC remains available, then stop receiver intake and close storage. Bound waits and surface failures.
- Document the command, destination, exclusions and shutdown behavior; add a tracked fixture-backed TODO.

## Verification

- Ginkgo tests cover routing, status, parent propagation and sampling, ingestion exclusion, streaming/WebSockets, loopback validation, export failures and shutdown.
- Live API tests prove opt-in behavior, automatic receiver startup, custom ports, searchable spans, and final-span durability after graceful shutdown and restart.
- Browser-check self traces and native metadata in `/otel` with agent-browser at desktop and compact widths.
- Run focused race tests, production build and lint; report pre-existing lint violations separately. Preserve unrelated dirty changes and do not commit.
