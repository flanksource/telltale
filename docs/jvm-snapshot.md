# JVM support snapshots

`telltale jvm snapshot` creates a ZIP of diagnostic evidence from a Java process. Select a connected agent or a local PID. PID selection attaches the bundled agent when necessary and reuses an existing agent without changing its configuration. Attachment requires a compatible JDK and permission to attach.

```bash
telltale jvm snapshot --pid 1234 --output support.zip
telltale jvm snapshot --agent 1234 --jfr 30s --heap-histogram --output profiled-support.zip
telltale jvm snapshot --server http://localhost:8080 --agent hub/orders --output orders-support.zip
```

`--agent` selects a connected Java agent; `--pid` selects a JVM on the machine running the command. They are mutually exclusive. `--java-home` chooses a target-compatible JDK for attachment. `--server` sends collection to a Telltale hub; set `TELLTALE_API_TOKEN` when it requires API authentication. `--output` names a ZIP that must not already exist; omitted, a timestamped filename is generated. Files are created with owner-only permissions.

The bundle contains OS/JVM properties and environment, runtime and operating-system MBeans, filesystem capacity, Linux process and cgroup settings, effective HotSpot VM flags, memory/GC metrics, thread stacks and deadlocks, class loaders, logger levels, agent identity and diagnostic status. `summary.md` describes collection outcomes. `manifest.json` records schema version, collection times, completion status and SHA-256 hashes of evidence files. Unsupported or denied collectors produce explicit errors rather than invented values. Linux-specific resources are marked not applicable on other operating systems.

Known secret properties, environment values, argument assignments and embedded credentials are masked before archive serialization. JFR uses a restricted built-in CPU/allocation/lock/GC event configuration, excluding arbitrary application events and raw environment/property/command-line events. Review diagnostic evidence before distributing it: paths, class names, thread names and application identifiers remain useful support information.

`--jfr` enables profiling for a positive duration up to five minutes, such as `30s`. The Telltale host requires a JDK 19+ `jfr` tool on PATH for export scrubbing, independently of the target JVM version. It rebuilds the recording to exclude events enabled by other recordings and removes unused constant-pool data. Raw intermediate recordings are held in a private directory under `.tmp/jvm-snapshot` and removed after scrubbing or failure. It owns and closes only its recording. `--heap-histogram` counts all objects, including unreachable objects, without requesting a full collection. Heap inspection can pause the JVM; cancelling the caller cannot interrupt a native heap walk already in progress. These collectors are disabled by default. Full heap dumps and application logs are not collected.

Collection failures preserve a partial ZIP and cause a nonzero CLI exit; inspect `summary.md`. Invalid requests and unavailable targets fail before collecting evidence. Existing destination files are never overwritten.

The JVM Diagnostics Snapshot tab provides the same options, cancellation and a download link. `POST /api/v1/jvm/snapshot` accepts `{ "agent": "hub/orders", "jfr": "30s", "heapHistogram": true }` and returns `application/zip`. Both optional fields may be omitted. `X-Telltale-Snapshot-Status` is `complete` or `partial`. A valid partial archive uses HTTP 200 so callers can retain the evidence; invalid input uses HTTP 400. Local filesystem output paths are not accepted through HTTP. The endpoint inherits API authentication and same-origin protection.
