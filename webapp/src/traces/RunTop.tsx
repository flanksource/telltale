import { useEffect, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Tabs } from "@flanksource/clicky-ui/components";
import { TopView, serverFiltersToFilterBar, type DataTableFilterSelection, type SampleFrame, type TopViewState, type WeightedHierarchy } from "@flanksource/clicky-ui/data";
import type { ClickyDocument, ClickyRow } from "@flanksource/clicky-ui/clicky";
import { failure, fetchTraceKinds, traceRecordsUrl, type CaptureResult, type RunTraces, type TraceKindInfo } from "./api";
import { recordQueryColumns, type RecordProfileColumn } from "./recordQuery";
import { TopRowDetail } from "./TopRowDetail";
import { ProfileSnapshotStatus } from "./ProfileSnapshotStatus";

interface SampleState extends TopViewState { selected: string; kind: string; stream: string; sort: string; desc: boolean; filters: DataTableFilterSelection }
interface TopResponse { selection: string; frame: SampleFrame; frames?: SampleFrame[]; rows: Record<string,unknown>[]; table: ClickyDocument; hierarchy?: WeightedHierarchy; metric?: string; metrics?: NonNullable<TraceKindInfo["top"]>["metrics"]; count: number; nextCursor?: string }

function readState(): SampleState {
  const params = new URLSearchParams(window.location.search);
  const get = (name: string) => params.get(`sample${name}`) ?? "";
  const filters = Object.fromEntries(params.getAll("sampleFilter").map(value => {
    const index = value.indexOf("=");
    if (index < 1) throw new Error("Invalid sample filter");
    return [value.slice(0, index), value.slice(index + 1)];
  }));
  return { selected:get("Selected"), kind: get("Kind"), stream: get("Stream"), frame: get("Frame"), source: get("Source"), metric: get("Metric"), view: get("View"), search: get("Search"), zoom: get("Zoom"), sort: get("Sort"), desc: get("Desc") === "1", paused: get("Paused") === "1", filters };
}

function useSampleState() {
  const [state, setState] = useState(readState);
  useEffect(() => { const restore = () => setState(readState()); window.addEventListener("popstate", restore); return () => window.removeEventListener("popstate", restore); }, []);
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    for (const key of Array.from(params.keys())) if (key.startsWith("sample")) params.delete(key);
    for (const [key, value] of Object.entries(state)) if (key !== "filters" && value) params.set(`sample${key[0]!.toUpperCase()}${key.slice(1)}`, value === true ? "1" : String(value));
    for (const [key, value] of Object.entries(state.filters)) if (value) params.append("sampleFilter", `${key}=${value}`);
    window.history.replaceState(null, "", `${window.location.pathname}?${params}${window.location.hash}`);
  }, [state]);
  return [state, (change: Partial<SampleState>) => setState(previous => ({ ...previous, ...change }))] as const;
}

async function loadJSON<T>(url: string, signal: AbortSignal, schema = false): Promise<T> {
  const response = await fetch(url, { signal, headers: { Accept: schema ? "application/schema+json" : "application/json" } });
  if (!response.ok) throw await failure(response, "Read sample");
  return response.json() as Promise<T>;
}

function CaptureTop({ run, capture, info, state, onChange }: { run: RunTraces; capture: CaptureResult; info: TraceKindInfo; state: SampleState; onChange: (change: Partial<SampleState>) => void }) {
  const [cursor, setCursor] = useState("");
  const [previous, setPrevious] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState(100);
  const params = new URLSearchParams({ view: "top", frame: state.frame, source: state.source, metric: state.metric, q: state.search, sort: state.sort, desc: String(state.desc), limit: String(pageSize) });
  for (const [key, value] of Object.entries(state.filters)) if (value) params.append("filter", `${key.replace(/^filter\./, "")}=${value}`);
  const scope = params.toString();
  const pageScope = new URLSearchParams(params);
  pageScope.delete("frame");
  const resetScope = pageScope.toString();
  useEffect(() => { setCursor(""); setPrevious([]); }, [resetScope]);
  const live = (run.status === "running" || !!run.standing) && !state.paused && !state.frame;
  const frames = useQuery({ queryKey: ["sample-frames", run.runId, capture.ref.stream], queryFn: ({ signal }) => loadJSON<SampleFrame[]>(traceRecordsUrl(run.mount, capture.ref, { view: "frames" }), signal), refetchInterval: live ? 1000 : false, retry: false });
  const schema = useQuery({ queryKey: ["sample-schema", run.runId, capture.ref.stream, state.frame], queryFn: ({ signal }) => loadJSON<{ columns: RecordProfileColumn[] }>(traceRecordsUrl(run.mount, capture.ref, { view: "top", frame: state.frame }), signal, true), retry: false });
  const url = new URL(traceRecordsUrl(run.mount, capture.ref), window.location.origin);
  for (const [key, value] of params) url.searchParams.append(key, value);
  if (cursor) url.searchParams.set("cursor", cursor);
  const result = useQuery({ queryKey: ["sample-top", run.runId, capture.ref.stream, scope, cursor], queryFn: ({ signal }) => loadJSON<TopResponse>(url.pathname + url.search, signal), refetchInterval: live ? 1000 : false, placeholderData: keepPreviousData, retry: false });
  const config = serverFiltersToFilterBar(recordQueryColumns(schema.data?.columns ?? []), state.filters, next => onChange({ filters: typeof next === "function" ? next(state.filters) : next }));
  const data=result.data;
  const statusFrames = live ? (frames.data ?? []).filter(frame => !state.source || frame.source === state.source) : data?.frames ?? (data ? [data.frame] : []);
  const rowID = (row: ClickyRow) => {
    const raw = data?.rows.find(value => value.seq === row.cells.seq?.filterValue);
    if (!raw) throw new Error("Top row is missing its source values");
    return JSON.stringify((info.top?.identity?.length ? info.top.identity : ["seq"]).map(key => {
      if (raw[key] === undefined || raw[key] === null) throw new Error(`Top row is missing identity ${key}`);
      return raw[key];
    }));
  };
  const change = (next: Partial<SampleState>) => {
    if (next.frame !== undefined || next.paused !== undefined) { setCursor(""); setPrevious([]); }
    onChange(next.paused === true && result.data ? { ...next, frame: result.data.selection } : next.paused === false ? { ...next, frame: "" } : next);
  };
  return <>
    <ProfileSnapshotStatus frames={statusFrames} live={live} />
    <TopView frames={frames.data ?? []} perSource={!!info.top?.perSource} metrics={result.data?.metrics ?? info.top?.metrics ?? []} state={state} onChange={change} onRefresh={() => { void result.refetch(); void frames.refetch(); }} loading={result.isFetching}
    {...(result.data ? { frame: result.data.frame, table: result.data.table } : {})} {...(result.data?.hierarchy ? { hierarchy: result.data.hierarchy } : {})}
    {...(result.data?.frames ? {selectedFrames:result.data.frames} : {})}
    {...(result.error || frames.error || schema.error ? { error: (result.error ?? frames.error ?? schema.error)!.message } : {})}
    tableProps={{ externalFilters: config.filters, ...(config.timeRange ? { timeRange: config.timeRange } : {}), sort: state.sort ? { key: state.sort, dir: state.desc ? "desc" : "asc" } : null,
      ...(data ? {renderRowDetail:(raw:Record<string,unknown>)=><TopRowDetail key={String(raw.seq)} raw={raw} table={data.table} mount={run.mount} capture={capture} selection={data.selection} relation={info.top?.relation} />} : {}),
      rowSelection: {selectedRowIds:state.selected ? [state.selected] : [], getRowId:rowID, selectAll:false, onSelectionChange:ids=>onChange({selected:ids.at(-1) ?? ""})},
      onTableRowClick: row => { if (result.data) onChange({frame:result.data.selection, paused:true, selected:rowID(row)}); },
      onSortChange: sort => onChange({ sort: sort?.key ?? "", desc: sort?.dir === "desc" }),
      pagination: { page: previous.length, pageSize, total: result.data?.count ?? 0, hasMore: !!result.data?.nextCursor, onPageSizeChange: setPageSize, onPageChange: page => {
        if (page < previous.length) { setCursor(previous[page] ?? ""); setPrevious(previous.slice(0, page)); }
        else if (result.data?.nextCursor) { setPrevious([...previous, cursor]); setCursor(result.data.nextCursor); onChange({ frame: result.data.selection, paused: true }); }
      } },
    }} />
  </>;
}

export function RunTop({ run }: { run: RunTraces }) {
  const [state, onChange] = useSampleState();
  const kinds = useQuery({ queryKey: ["sample-kinds", run.mount], queryFn: () => fetchTraceKinds(run.mount), retry: false });
  const allCaptures = [...(run.captures ?? [])];
  for (const ref of run.process ? [run.process.tree, run.process.stacks] : []) if (!allCaptures.some(item => item.ref.stream === ref.stream)) allCaptures.push({ kind: ref.kind, ref });
  const captures = allCaptures.filter(capture => kinds.data?.some(info => info.name === capture.kind && info.top));
  const capture = captures.find(item => item.kind === state.kind && (!state.stream || item.ref.stream === state.stream)) ?? captures[0];
  const info = kinds.data?.find(item => item.name === capture?.kind);
  if (kinds.error) return <p role="alert">{kinds.error.message}</p>;
  if (!capture || !info) return <p role="status">{kinds.isPending ? "Reading sample capabilities…" : "This run has no sampled captures. Open History to inspect its records."}</p>;
  return <div className="flex h-full min-h-0 flex-col gap-2 overflow-hidden">
    <Tabs tabs={captures.map(item => ({ id: item.ref.stream, label: item.kind }))} value={capture.ref.stream} onChange={stream => { const next = captures.find(item => item.ref.stream === stream); if (!next) throw new Error("Unknown sampled capture"); onChange({ kind: next.kind, stream, frame: "", source: "", metric: "", zoom: "", view: "", filters: {}, search: "", sort: "", paused: false }); }} />
    <CaptureTop key={capture.ref.stream} run={run} capture={capture} info={info} state={state} onChange={onChange} />
  </div>;
}
