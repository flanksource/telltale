import { useEffect, useRef, useState } from "react";
import { Button, Checkbox, InputField, Panel } from "@flanksource/clicky-ui/components";
import { persistFilter } from "./api";
import { failure } from "../traces/api";

export function JvmSnapshot({ agent }: { agent: string }) {
  const query = new URLSearchParams(window.location.search);
  const [jfr, setJfr] = useState(query.get("snapshotJfr") === "true");
  const [duration, setDuration] = useState(query.get("snapshotDuration") ?? "30s");
  const [histogram, setHistogram] = useState(query.get("snapshotHistogram") === "true");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const [download, setDownload] = useState<{ url: string; partial: boolean }>();
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => () => { if (download) URL.revokeObjectURL(download.url); }, [download]);
  async function collect() {
    const abort = new AbortController();
    controller.current = abort;
    setPending(true); setError(undefined); setDownload(undefined);
    try {
      const response = await fetch("/api/v1/jvm/snapshot", {
        method: "POST", headers: { "Content-Type": "application/json" }, signal: abort.signal,
        body: JSON.stringify({ agent, ...(jfr ? { jfr: duration } : {}), heapHistogram: histogram }),
      });
      if (!response.ok) throw await failure(response, "Collect JVM snapshot");
      setDownload({ url: URL.createObjectURL(await response.blob()), partial: response.headers.get("X-Telltale-Snapshot-Status") === "partial" });
    } catch (cause) { setError(abort.signal.aborted ? "Snapshot cancelled." : cause instanceof Error ? cause.message : String(cause)); }
    finally { setPending(false); controller.current = undefined; }
  }
  const durationMatch = /^(\d+(?:\.\d+)?)(ms|s|m)$/.exec(duration);
  const milliseconds = durationMatch ? Number(durationMatch[1]) * (durationMatch[2] === "m" ? 60000 : durationMatch[2] === "s" ? 1000 : 1) : 0;
  const validDuration = milliseconds > 0 && milliseconds <= 300000;
  return <Panel title="Support snapshot"><form className="space-y-4" onSubmit={event => { event.preventDefault(); void collect(); }}>
    <p className="text-sm">Collect OS/JVM settings, system properties, environment, resource usage and limits, metrics, thread dump, class loaders, logging levels and diagnostic status for agent <code>{agent}</code>. Known secrets are masked.</p>
    <label className="flex items-center gap-2"><Checkbox checked={jfr} disabled={pending} onChange={value => { setJfr(value); persistFilter("snapshotJfr", String(value)); }} />Include JFR profiling</label>
    {jfr && <label className="block text-sm">Recording duration<InputField value={duration} disabled={pending} onChange={value => { setDuration(value); persistFilter("snapshotDuration", value); }} />{!validDuration && <span role="alert">Enter a positive duration, such as 30s (maximum 5m).</span>}</label>}
    <label className="flex items-center gap-2"><Checkbox checked={histogram} disabled={pending} onChange={value => { setHistogram(value); persistFilter("snapshotHistogram", String(value)); }} />Include heap histogram</label>
    {histogram && <p className="text-sm text-muted-foreground">Counts all objects, including unreachable objects. Heap inspection may pause the JVM; cancelling cannot interrupt a native heap walk already in progress.</p>}
    <div className="flex gap-2"><Button type="submit" disabled={pending || (jfr && !validDuration)}>{pending ? "Collecting snapshot…" : "Create support bundle"}</Button>{pending && <Button type="button" variant="outline" onClick={() => controller.current?.abort()}>Cancel</Button>}</div>
    {pending && <p role="status">Collecting diagnostic evidence{jfr ? ` and ${duration} of profiling` : ""}…</p>}
    {error && <p role="alert">{error}</p>}
    {download && <div className="space-y-2"><p role={download.partial ? "alert" : "status"}>{download.partial ? "Partial bundle: some collectors failed. See summary.md for details." : "Support bundle ready."}</p><a className="underline" href={download.url} download="telltale-jvm-snapshot.zip">Download support ZIP</a></div>}
  </form></Panel>;
}
