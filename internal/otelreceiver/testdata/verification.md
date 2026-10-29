---
cwd: ../../..
build: make build
timeout: 10m
---

# OTLP receiver verification

The Go suites verify codecs, redaction, native metadata, exact integers, durable storage, retry/conflict behavior, retention, SDK exporters, authentication, overload and draining. The live API suite exercises the built binary, startup failure, source discovery, complete trace search, restart and non-loopback HTTP authentication. Browser evidence is recorded separately in the implementation checkpoint; these commands reproduce the automated contract.

| Name | Command | Exit Code | CEL Validation |
| --- | --- | --- | --- |
| Receiver and trace-query integration | go test ./internal/otelreceiver ./internal/tracequery -ginkgo.no-color | 0 | exitCode == 0 |
| Receiver API and viewer interactions | pnpm exec vitest run packages/api/test/receiver.spec.ts webapp/src/otel | 0 | exitCode == 0 |
| Frontend lint | pnpm lint | 0 | exitCode == 0 |
| Frontend types | pnpm typecheck | 0 | exitCode == 0 |

`make lint` is a separate repository-wide gate. It currently reports five existing Go violations in `internal/cli/records.go`, `pkg/traces/read/follow.go`, `pkg/traces/read/rows.go`, and `pkg/tracing/sqlserver/plancache/schema_spec_test.go`. No suppression or baseline change is part of this feature.
