# Collector Profiles compatibility fixture

This isolated module pins the real OTLP receiver and queued OTLP exporter to Collector `0.163.0`. The parent Ginkgo integration test starts the fixture, sends HTTP Profiles to `/v1development/profiles`, and verifies snapshots delivered over gRPC into Telltale. It does not replace either Collector component with a proxy.

Run from the repository root:

```sh
go test ./internal/otelreceiver -run TestReceiver -ginkgo.focus='released Collector Profiles pipeline'
```

The gate checks active, updated and empty observations through request-sized and item-sized export queues. Telltale convention version `2` encodes an empty observation with one explicit zero-valued `snapshot_control` sample because the released Collector discards zero-sample profiles. The test verifies that the native control survives and that the latest Top view contains zero operation rows. Generic Profiles consumers and Collector sample counters still see the control sample; see the [wire contract](../../docs/traces/profiles.mdx).

The parent process isolates this module with `GOWORK=off` so local workspace modules do not substitute the pinned Collector dependencies. Compatibility assertions remain enabled.

`main.go` waits for standard input to close, then shuts down the receiver and drains the exporter. Dependencies remain isolated from the application module.
