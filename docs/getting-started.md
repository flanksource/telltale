# Quick start

Telltale combines a CLI and an HTTP server with a browser workspace. This guide builds it from a source checkout and records a command.

## Build and start the server

Install Go 1.27.1, Node 24, pnpm 11.22.0, a JDK (17 or 21 recommended), [Task](https://taskfile.dev/installation/), Gavel, and actionlint. Then run from the repository root:

```sh
make setup
make build
.bin/telltale serve
```

Open `http://localhost:8080`. The server also exposes `/health` and `/api/openapi.json`. Use `--host` and `--port` to change the listener.

## Record a command

In another terminal, run:

```sh
.bin/telltale trace run -- curl -sS https://example.com
.bin/telltale trace list
```

The default capture uses the HTTP proxy seam, and Telltale also records the process tree. Open the web workspace to inspect the run. Add `--follow --format ndjson` to print captured records as they arrive, or run `.bin/telltale trace run --help` for the available capture kinds and parameters.

Run `.bin/telltale explain` to list trace kinds, or `.bin/telltale explain http` to inspect a kind's parameters and record schema. Add `--json` for structured output.

Runs are stored under `traces/<run id>` in the data directory. Set `--data-dir` to choose it; by default Telltale uses `$XDG_CACHE_HOME/telltale` or the operating system cache directory.

## Next steps

- Learn how to [navigate the web workspace](/web-ui).
- [Receive OpenTelemetry traces](/otlp-receiver) from an instrumented application.
- [Preview resource monitoring rules](/rules) before enabling hooks.
- Consult the [README](https://github.com/flanksource/telltale#readme) for trace definitions, connections, process grouping, and development commands.

## Work on this documentation

From the repository root, install the documentation site's locked dependencies and start its development server:

```sh
npm --prefix docs ci
npm --prefix docs run dev
```

Run `npm --prefix docs run build` to generate the static site in `docs/doc_build`. Preview the build with `npm --prefix docs run preview`.

The trace-kind argument and record schemas are generated from Telltale's registered catalog. After changing a kind, run `npm --prefix docs run generate:catalog` and commit the updated snapshot. The documentation build checks that the snapshot is current.
