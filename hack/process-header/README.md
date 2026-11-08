# Process header demo

Run a local workload with changing CPU/RAM, repeated HTTP requests, and a child process that exits after two seconds:

```sh
.bin/telltale trace run --data-dir .tmp/process-header/data --tui -- go run ./hack/process-header --duration 30s
```

The process rows update above the event stream. Press `e` to show exited processes, Alt+↑/↓ to scroll process rows, and Space to pause events while process measurements continue updating. The command prints its saved run ID; open it afterward with:

```sh
.bin/telltale trace replay <run-id> --data-dir .tmp/process-header/data --ui
```

The web viewer uses the same process measurements and Go pretty printers. Show exited reveals the child’s last sample. All network requests remain on localhost, and no credentials or external services are needed.
