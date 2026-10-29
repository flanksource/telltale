---
cwd: ../../..
build: make build
timeout: 10m
---

# Self-tracing acceptance

| Name | Command | Exit Code | CEL Validation |
| --- | --- | --- | --- |
| Middleware and real local export | go test -race ./internal/selftrace -ginkgo.no-color | 0 | exitCode == 0 |
| HTTP drain and receiver lifecycle | go test -race ./internal/server -ginkgo.focus='self-trace lifecycle' -ginkgo.no-color | 0 | exitCode == 0 |
| Self-trace and receiver HTTP APIs | pnpm exec vitest run packages/api/test/selftrace.spec.ts packages/api/test/receiver.spec.ts | 0 | exitCode == 0 |
| Frontend lint | pnpm lint | 0 | exitCode == 0 |
| Frontend types | pnpm typecheck | 0 | exitCode == 0 |

Run `make lint` separately and report the five existing Go violations without suppressions. Live agent-browser checks confirm the existing `/otel` viewer displays self traces at desktop and compact widths.
