# Automatic agent startup

Import `github.com/flanksource/telltale/sdk/auto` for its side effects to start the SDK when `TELLTALE_AGENT=1` or `TELLTALE_URL` is set. The package instruments `http.DefaultClient.Transport` while preserving `http.DefaultTransport`. Startup failures are logged and do not panic the application.

Create a hub credential in the server's work directory with `telltale agents token create <name> --work-dir <directory>`. Supply the printed token to the application as `TELLTALE_TOKEN` and the server address as `TELLTALE_URL`. The server stores only the token hash under the work directory's `.telltale/agent-tokens` directory.

Agent tokens only authorize connections to the hub. For a server bound to a non-loopback address, create an API token with `telltale agents token create <name> --scope api --work-dir <directory>` and send it as a bearer token for API requests.

Use `telltale agents list --server <url>` and `telltale agents get <id> --server <url>` to inspect agents connected to that server.
Set `TELLTALE_API_TOKEN` to the API token when accessing a non-loopback server with those commands.
