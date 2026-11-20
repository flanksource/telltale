# Completed sample Top views

Top reads one completed sample before applying search, column filters, ordering and pagination. A successful empty poll replaces the previous sample. Interrupted reads publish no completion record. History and native JSON exports retain every captured row. Recordings without completion metadata show “Complete samples unavailable” and remain readable in History.

The kind catalog advertises Top capabilities for `sql_top`, `sql_plancache`, `process`, `goprof`, `jvm_jfr`, `prometheus`, `stacks` and `query`. SQL activity defaults to elapsed time, plan-cache to interval CPU, process observations to CPU, and stacks to waiting time. Prometheus combines the latest completed scrape of each endpoint. Query captures retain the originating profile's columns, filter grammar and order; repeated reads remain incremental windows.

Use `-i` or `--interactive` to fill a command's arguments in Clicky's JSON Schema form before it runs. Saved connections appear in the Connection chooser, limited to the types that kind accepts: PostgreSQL and SQL Server for `sql_top`, and SQL Server for `sql_plancache`. Existing flags and arguments prefill the form. Tab moves between fields, Enter edits a field, Ctrl+S submits, and Ctrl+C cancels before capture starts. The web Arm captures form uses the same schema metadata and saved-connection lookup.

```sh
.bin/telltale run sql_top -i
.bin/telltale run sql_top -i --duration 30s --poll 2s
.bin/telltale run sql_plancache -i
```

```sh
make build
.bin/telltale records sql_top --run <run-id> --view top --filter 'elapsedMs=>1s' --limit 20
.bin/telltale records sql_top --run <run-id> --view top --frame <sample-id> --sort elapsedMs --desc --json
.bin/telltale trace replay <run-id> --tui
.bin/telltale trace replay <run-id> --ui
```

In the web viewer, choose a source, sample and metric in Top. CPU profiles use flamegraphs and memory uses treemaps. Process graphs aggregate exclusive process usage through ownership groups; resident memory sums process RSS rather than physical host memory. Search, column filters, metric, zoom, sample, sort and selected row are saved in the URL. Pause, pagination and detail inspection pin the current selection. Resume live explicitly to return to the newest completed sample. SQL detail includes copyable SQL and a blocker lookup within the same sample; an omitted blocker is identified explicitly.

The TUI uses `s` to change sort, `S` to reverse, `/` for search, `f` for a native `column=expression` filter, `m` for metric, `o` for source, `c` for capture, and `p/n` for historical samples. Enter opens scrollable detail and freezes that sample. `b` follows a blocker, `h` opens History, `r` resumes live, and `?` lists controls. Standalone process Top preserves grouping and guarded process actions; `d` opens frozen detail, `P` pauses/resumes, and `?` shows help.

The records route accepts `view=frames` to list completion metadata and `view=top` with `frame`, `source`, `metric`, `q`, repeatable `filter`, `where`, `sort`, `desc`, `limit` and `cursor`. Its Top response includes `selection`, the exact frame selection to send when continuing a page or freezing multiple endpoint samples. `Accept: application/schema+json` with `view=top` returns the same native column contract used for reads. Unavailable SQL measurements are null; measured zero remains zero.

```sh
go run ./hack/top-fixture
.bin/telltale serve --dev --port 8080 --data-dir .tmp/top-ux-review/data
```

The fixture creates an embedded local PostgreSQL database, records a lock holder and waiter, then closes the database. Its output names the new recording. It writes only under `.tmp/top-ux-review/`.

The reusable implementations live in Clicky (`tui/table`), commons-db (`recordstore/sampleframe`, typed query presentation and SQL activity), and Clicky UI (`TopView`, `WeightedProfile`). Consumers must use released package versions for production. Development uses the existing Go workspace and `serve --dev` sibling-source resolution; local replacement directives must not be published.
