# Telltale

A Flanksource Go CLI and HTTP server with a React frontend built with Vite and Clicky UI.

## Setup

Install Go 1.27.1, Node 24, pnpm 11.22.0, [Task](https://taskfile.dev/installation/), Gavel, and actionlint. The root `packageManager` field pins pnpm; enable your package manager's version management before installing.

```sh
make setup
make build
.bin/telltale serve
```

Open `http://localhost:8080`. The server exposes `/health` and `/api/openapi.json`. Use `--host` and `--port` to change the API listener; port `0` requests a free port. `--version` reports the build version, which defaults to `dev` outside releases.

## Development with local Clicky UI

Install the sibling Clicky UI checkout's dependencies using its own setup instructions, then start Telltale:

```sh
make dev
```

This runs `telltale serve --dev`, starts Vite on `http://localhost:5173`, and proxies `/api` and `/health` to Go. Open the Vite URL for HMR. The Go URL also proxies frontend requests. Ctrl+C stops both servers. Go changes require restarting the command.

`--dev` resolves the sibling `../clicky-ui/packages/ui/src` directly, including public component entrypoints, styles, and theme configuration. It reports an error when the selected source checkout is missing. To use another Clicky UI worktree:

```sh
TELLTALE_CLICKY_UI_DIR=/path/to/clicky-ui task dev
```

Set `--ui-port` to change the Vite port and `--web-dir` when launching outside the repository root:

```sh
task dev -- --port 8180 --ui-port 5174
```

Production builds and normal frontend tests use published Clicky UI packages from the lockfile. A plain source checkout compiles with an embed sentinel but cannot serve the production UI until `make build` has generated it.

## Checks and build output

```sh
make lint
make test
make clean
```

`make test` builds first, runs Ginkgo through Gavel, and runs Vitest unit and live API tests. The Go binary is written to `.bin/telltale`; frontend bundles go to `webapp/dist`. Both are ignored. `make binary` compiles existing assets without Node or pnpm. `VERSION=v0.0.1 make build` sets the binary version explicitly.

Vite 8 uses Rolldown for bundling. The app keeps the React runtime in a shared chunk, loads backend status through a dynamic import, and retains automatic dependency and CSS splitting. HTML is revalidated; hashed assets use immutable caching.

## Releases

After main passes CI, a serialized release job checks out the exact verified source SHA, builds assets using locked published dependencies, and validates the release candidate. It creates one detached commit containing only `webapp/dist`, tags that commit, verifies the tagged Go build, and pushes only the tag. Main receives no generated asset commits.

```text
main:        A --- B --- C
                   \
                    assets commit <- v0.0.1
```

Versions use the same all-tags `svu patch` convention as Gavel, starting at `v0.0.1`. A retry reuses an existing tag only when its parent matches the verified source and its diff contains only assets. Tags are never force-moved. Release preparation is covered by disposable Git-repository tests and is not a command to run in a developer checkout.

Tagged source includes all embedded files, so installation needs only Go:

```sh
go install github.com/flanksource/telltale/cmd/telltale@v0.0.1
```

No release has been created by repository initialization. The example tag becomes usable only after the first successful release job.
