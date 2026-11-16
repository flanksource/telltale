# Replay recorded traces

`telltale trace replay` reads the saved captures of one or more trace/run IDs. It never executes the recorded commands. Find IDs with `telltale trace list`, then pass them as positional arguments:

```sh
telltale trace list
telltale trace replay buildhost20261107T123000Z
telltale trace replay buildhost20261107T123000Z buildhost20261107T123000Z_2 --ui
telltale trace replay buildhost20261107T123000Z --tui
telltale trace replay buildhost20261107T123000Z --format ndjson --sort timestamp
```

Replace the example IDs with ones from your data directory. Every form accepts `--data-dir` when the recordings live outside the default cache directory. Unknown IDs, duplicate selections, invalid paths and recordings without a readable manifest fail before any records are printed.

Terminal output reads every page, merging captures by `--sort seq` (the default) or `--sort timestamp`. Sequence numbers belong to individual streams. NDJSON records include `runId`, `kind`, `stream`, `seq`, `timestamp`, `summary` and the original payload under `record`. `--follow` reads and follows selected captures until their streams seal; live arrivals preserve each stream's order.

`--ui` opens a loopback viewer restricted to the selected runs and serves until Ctrl+C. A **Run** selector switches between multiple recordings and retains the selection in the URL. Kind-specific rendering opens in **Captures**; **Raw stream** opens the record table and JSON details on demand. `--tui` opens the full-screen kind monitors and separate stdout/stderr tabs. Tab/Shift+Tab switches kinds; `[` and `]` switch selected runs, retaining each run's tab state. Older combined recordings display their saved output in Stdout and explicitly report that separate stderr is unavailable. Attached processes without recorded output show an unavailable message. Choose one of `--ui` and `--tui`.

New trace IDs concatenate up to nine characters from the short hostname with a compact UTC timestamp: `buildhost20261107T123000Z`. Hostname dashes are removed, and the timestamp contains no dashes. Runs created on the same host in the same second receive `_2`, `_3`, and subsequent suffixes. Existing recordings keep their IDs and remain selectable. Listing and retention use recorded start times across hostnames and ID formats; stores without a manifest use their directory modification time for retention.
