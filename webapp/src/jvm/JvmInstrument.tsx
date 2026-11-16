import { useState } from "react";
import { Button, SegmentedControl, Workspace } from "@flanksource/clicky-ui/components";
import { defaultProbeOptions, filterError, methodCaptureRequest, readMethodTargets, writeMethodTargets } from "./captures";
import { JvmCallFilters } from "./JvmCallFilters";
import { fieldClass } from "./diagnostics";
import { MutationChoice } from "./MutationChoice";
import { JvmMethodTargets } from "./JvmMethodTargets";
import { JvmCaptureResults } from "./JvmCaptureResults";
import { persistFilter } from "./api";
import { useRecordings } from "./useRecordings";

export function JvmInstrument({ agent }: { agent: string }) {
  const [targets, setTargets] = useState(() => readMethodTargets(window.location.search));
  const [options, setOptions] = useState(defaultProbeOptions);
  const [duration, setDuration] = useState("5m");
  const [allowed, setAllowed] = useState(false);
  const [selected, setSelected] = useState(() => new URLSearchParams(window.location.search).get("recording") ?? "");
  const { query, recordings, start, stop } = useRecordings(agent, "jvm_trace");
  const active = recordings.find(row => row.run.runId === selected);
  const invalidFilter = filterError(options);
  const ready = allowed && !invalidFilter && !!duration.trim() && targets.length > 0 && targets.every(row => row.class.trim() && row.method.trim() && !row.loader.startsWith("~"));
  const configuration = <form className="space-y-4" onSubmit={event => { event.preventDefault(); start.mutate(methodCaptureRequest({ agent, targets, options, duration, allowed }), { onSuccess: run => setSelected(run.runId) }); }}>
    <JvmMethodTargets agent={agent} targets={targets} onChange={next => { setTargets(next); const search = writeMethodTargets(window.location.search, next); window.history.replaceState(null, "", `${window.location.pathname}?${search}`); }} />
    <div className="space-y-2"><h3 className="text-sm font-medium">Mode</h3><SegmentedControl aria-label="Instrumentation mode" value={options.calls ? "trace" : "watch"} options={[{ id: "watch", label: "Watch values" }, { id: "trace", label: "Trace nested calls" }]} onChange={mode => setOptions(current => ({ ...current, calls: mode === "trace", args: mode === "watch", return: mode === "watch", platform: false }))} /></div>
    <div className="grid gap-4 md:grid-cols-3">
      <fieldset className="space-y-2"><legend className="mb-2 text-sm font-medium">What to record</legend>{(["args", "return", "exception", "calls", "platform", "gc", "alloc"] as const).map(key => <label key={key} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={options[key]} disabled={key === "platform" && !options.calls} onChange={event => setOptions(current => ({ ...current, [key]: event.target.checked, ...(key === "calls" && !event.target.checked ? { platform: false } : {}) }))} />{({ args: "Arguments", return: "Return value", exception: "Exceptions", calls: "Nested calls", platform: "Platform calls", gc: "GC counters", alloc: "Allocation counters" })[key]}</label>)}</fieldset>
      <fieldset className="space-y-3"><legend className="mb-2 text-sm font-medium">Limits</legend>
        <label className="block text-sm">Stop after calls<select className={fieldClass} value={options.events} onChange={event => setOptions({ ...options, events: Number(event.target.value) })}>{[1, 10, 100, 1000, 2147483647].map(value => <option key={value} value={value}>{value === 2147483647 ? "Until duration ends" : value}</option>)}</select></label>
        <label className="block text-sm">Expand values<select className={fieldClass} value={options.expand} onChange={event => setOptions({ ...options, expand: Number(event.target.value) })}>{Array.from({ length: 10 }, (_, index) => index + 1).map(value => <option key={value} value={value}>{value} levels</option>)}</select></label>
        <label className="block text-sm">String character limit<select className={fieldClass} value={options.chars} onChange={event => setOptions({ ...options, chars: Number(event.target.value) })}>{[1024, 4096, 16384, 65536].map(value => <option key={value}>{value}</option>)}</select></label>
      </fieldset>
      <fieldset className="space-y-3"><legend className="mb-2 text-sm font-medium">Automatic restore</legend><label className="block text-sm">Capture duration<input required className={fieldClass} value={duration} onChange={event => setDuration(event.target.value)} /></label><p className="text-xs text-muted-foreground">For example, 30s or 5m. Original bytecode returns when the capture stops, reaches its budget or its duration ends. Closing this page leaves the server-owned capture running.</p><MutationChoice label="Allow method instrumentation" checked={allowed} onChange={setAllowed} /></fieldset>
    </div>
    <JvmCallFilters filters={options} onChange={filters => setOptions(current => ({ ...current, ...filters }))} />
    {invalidFilter && <p role="alert" className="text-sm">{invalidFilter}</p>}
    {start.error && <p role="alert" className="text-sm">{start.error.message}</p>}
    <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-3"><p className="text-xs text-muted-foreground">Methods run their original bodies and report values and timings.</p><Button type="submit" disabled={!ready || start.isPending}>{start.isPending ? "Instrumenting…" : targets.length > 1 ? `Instrument ${targets.length} methods` : "Instrument method"}</Button></div>
  </form>;
  return <div className="space-y-3 min-w-0">
    <p className="text-sm text-muted-foreground">Instrument methods without compiling replacement source. Targets stay in the URL so you can reload or share this selection.</p>
    <label className="block text-sm">Recorded capture<select className={fieldClass} value={selected} onChange={event => { setSelected(event.target.value); persistFilter("recording", event.target.value); }}><option value="">Choose a recorded capture</option>{recordings.map(row => <option key={row.run.runId} value={row.run.runId}>{row.run.runId} · {row.run.status}</option>)}</select></label>
    {query.error && <p role="alert" className="text-sm">{query.error.message}</p>}
    <Workspace storageKey={`telltale.jvm.${agent}.instrument`} className="h-[1000px] rounded border" panes={[
      { id: "configuration", label: "Method instrumentation", location: "center", collapsible: false, contentClassName: "overflow-auto p-3", content: configuration },
      { id: "results", label: "Recorded method events", location: "bottom", height: 400, contentClassName: "flex min-h-0 flex-col overflow-hidden p-3", content: <JvmCaptureResults recording={active} stop={run => stop.mutate(run)} stopping={stop.isPending} error={stop.error} /> },
    ]} />
  </div>;
}
