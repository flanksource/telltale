# JMS messages and receive handlers

`telltale run jms --agent api` records actual sent and received messages. Empty receive polls and timeouts produce no message events. Failed sends with an observed Message remain error events. Bodies are disabled by default; `--body --max-body-bytes 65536` enables bounded text capture.

`telltale run jms_top --agent api --poll 1s --top 50` samples destination-to-handler mappings. Discovered handlers remain visible while idle. The active count and elapsed time describe handler processing, not a consumer waiting for a message. `--destinations orders` selects a destination. Handler mappings are discovered through observed execution; handlers that have never been observed cannot be enumerated from the JVM heap.

Use `--tui` for the fullscreen Top table. Without `--tui`, captures print human-readable records to stderr automatically. Structured results and explicit NDJSON following remain on stdout. `--no-color` disables styling. The web viewer and TUI show Top snapshots in their Top views, separate from All events.
