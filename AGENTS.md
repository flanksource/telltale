# Telltale

Go CLI and HTTP server with a React/Vite frontend. Keep command handlers thin and domain behavior under `internal/`. Reuse Clicky RPC and published Clicky UI components.

## Commands

- `make setup`: install locked dependencies.
- `make dev`: run Go and Vite against sibling Clicky UI source.
- `make build`: type-check and build frontend assets, then compile `.bin/telltale`.
- `make binary`: compile Go using existing assets without running frontend tooling.
- `make lint`: check Go, TypeScript, frontend code, workflows, and release script syntax.
- `make test`: build and run Go, Vitest, and live HTTP integration tests.

## Assets and releases

Keep generated `webapp/dist` files ignored on main; only `.gitkeep` is tracked. CI creates a detached asset-only commit above the exact verified source revision and pushes only its version tag. Never merge these release commits into main or force-move tags. `hack/release.sh` is exercised only in disposable repositories during tests; do not run it in a developer checkout.

Production builds always resolve Clicky UI from the pnpm lockfile. `serve --dev` uses `../clicky-ui/packages/ui/src` or the checkout specified by `TELLTALE_CLICKY_UI_DIR`; missing source is an error. Use agent-browser for live UI checks.
