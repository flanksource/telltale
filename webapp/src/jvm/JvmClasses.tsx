import { Select, InputField, Checkbox } from "@flanksource/clicky-ui/components";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { CodeBlock, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { startRun } from "../traces/api";
import { fieldClass, inspect, type ClassLoader, type ClassPage, type ClassSource, type Compilation } from "./diagnostics";
import { persistFilter } from "./api";
import { MutationChoice } from "./MutationChoice";
import { SourceEditor } from "./SourceEditor";
import { JvmResults } from "./JvmResults";

const pageSize = 100;
type ClassRow = ClassPage["classes"][number] & Record<string, unknown>;

export function JvmClasses({ agent }: { agent: string }) {
  const initial = new URLSearchParams(window.location.search);
  const [query, setQuery] = useState(initial.get("query") ?? "");
  const [loader, setLoader] = useState(initial.get("loader") ?? "");
  const [selected, setSelected] = useState(initial.get("class") ?? "");
  const [offset, setOffset] = useState(Number(initial.get("offset") ?? 0));
  const [platform, setPlatform] = useState(initial.get("includePlatform") === "true");
  const loaders = useQuery({ queryKey: ["jvm-loaders", agent], queryFn: () => inspect<ClassLoader[]>(agent, "jvm.loaders") });
  const classes = useQuery({ queryKey: ["jvm-class-page", agent, query, loader, offset, platform], queryFn: () => inspect<ClassPage>(agent, "jvm.classes", { query, ...(loader ? { loader } : {}), limit: pageSize, offset, includePlatform: platform }) });
  const columns: DataTableColumn<ClassRow>[] = [{ key: "name", label: "Class", minWidth: 220, render: (_, row) => <span className="break-all font-mono text-xs">{row.name}</span> }, { key: "loader", label: "Loader" }];
  const resetPage = () => { setOffset(0); persistFilter("offset", ""); };
  return <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(280px,1fr)_minmax(0,2fr)]">
    <Panel title="Loaded classes"><div className="space-y-3 min-w-0">
      <label className="block text-sm">Class filter<InputField value={query} onChange={value => { setQuery(value); persistFilter("query", value); resetPage(); }} /></label>
      <label className="block text-sm">Class loader<Select className={fieldClass} value={loader} onChange={event => { setLoader(event.target.value); persistFilter("loader", event.target.value); resetPage(); }}><option value="">All loaders</option>{loaders.data?.map(item => <option key={item.hash} value={item.hash}>{item.label} · {item.hash} · {item.classCount} classes</option>)}</Select></label>
      <label className="flex gap-2 text-sm"><Checkbox checked={platform} onChange={value => { setPlatform(value); persistFilter("includePlatform", String(value)); resetPage(); }} />Include platform classes</label>
      {(loaders.error || classes.error) && <p role="alert">{(loaders.error ?? classes.error)?.message}</p>}
      <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={classes.data?.classes ?? []} columns={columns} loading={classes.isPending} getRowId={row => `${row.loader}:${row.name}`} onRowClick={row => { setSelected(row.name); setLoader(row.loader); persistFilter("class", row.name); persistFilter("loader", row.loader); persistFilter("method", ""); }} emptyMessage="No matching loaded classes" /></div>
      <div className="flex flex-wrap items-center gap-2 text-sm"><Button variant="outline" disabled={offset === 0 || classes.isFetching} onClick={() => { setOffset(Math.max(0, offset - pageSize)); persistFilter("offset", String(Math.max(0, offset - pageSize))); }}>Previous</Button><span>{classes.data?.total ?? 0} classes · {classes.data?.classes.length ? `${offset + 1}–${offset + classes.data.classes.length}` : "0 shown"}</span><Button variant="outline" disabled={!classes.data || offset + pageSize >= classes.data.total || classes.isFetching} onClick={() => { setOffset(offset + pageSize); persistFilter("offset", String(offset + pageSize)); }}>Next</Button></div>
    </div></Panel>
    {selected ? <SourcePanel key={`${agent}:${selected}:${loader}`} agent={agent} className={selected} loader={loader} /> : <Panel title="Class source"><p className="text-sm text-muted-foreground">Select a loaded class to decompile its source, inspect methods and start a watch or leased redefinition.</p></Panel>}
  </div>;
}

function SourcePanel({ agent, className, loader }: { agent: string; className: string; loader: string }) {
  const client = useQueryClient();
  const { renderLink } = useRouter();
  const [method, setMethod] = useState(() => new URLSearchParams(window.location.search).get("method") ?? "");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [duration, setDuration] = useState("15m");
  const [allowed, setAllowed] = useState(false);
  const [compiled, setCompiled] = useState<{ source: string; result: Compilation }>();
  const source = useQuery({ queryKey: ["jvm-class-source", agent, className, loader], queryFn: () => inspect<ClassSource>(agent, "jvm.classes.source", { class: className, ...(loader ? { loader } : {}) }) });
  const target = { class: className, ...(source.data ? { loader: source.data.loader } : loader ? { loader } : {}) };
  const compile = useMutation({ mutationFn: (submitted: string) => inspect<Compilation>(agent, "jvm.classes.compile", { ...target, source: submitted }), onSuccess: (result, submitted) => setCompiled({ source: submitted, result }) });
  const redefine = useMutation({ mutationFn: () => startRun({ duration, traces: { jvm_redefine: { enabled: true, target: { seam: "agent", agent, allowMutation: allowed }, ...target, source: draft } } }), onSuccess: () => { setEditing(false); void client.invalidateQueries({ queryKey: ["jvm-class-source", agent] }); void client.invalidateQueries({ queryKey: ["jvm-patches", agent] }); void client.invalidateQueries({ queryKey: ["trace-runs"] }); } });
  const download = useMutation({ mutationFn: async (kind: "source" | "bytecode") => {
    if (kind === "source") { saveClassFile(`${className}.java`, new Blob([source.data!.source], { type: "text/x-java-source" })); return; }
    const value = await inspect<{ bytecode: string }>(agent, "jvm.classes.bytecode", target);
    const bytes = Uint8Array.from(atob(value.bytecode), character => character.charCodeAt(0));
    saveClassFile(`${className}.class`, new Blob([bytes], { type: "application/java-vm" }));
  } });
  const selection = new URLSearchParams({ class: className, ...(source.data ? { loader: source.data.loader } : {}), ...(method ? { method } : {}) });
  const focused = source.data?.methods.find(item => `${item.name}${item.descriptor}` === method || item.name === method);
  const displaySource = source.data ? focused ? source.data.source.split("\n").slice(focused.line - 1, focused.endLine).join("\n") : source.data.source : "";
  return <Panel title={className}>
    {source.isPending && <p role="status">Decompiling class…</p>}{source.error && <p role="alert">{source.error.message}</p>}
    {source.data && <div className="space-y-3 min-w-0">
      <p className="break-all text-xs text-muted-foreground">Loader {source.data.loader} · {source.data.decompiler} · {source.data.digest}</p>
      <label className="block text-sm">Jump to method<Select className={fieldClass} value={method} onChange={event => { setMethod(event.target.value); persistFilter("method", event.target.value); }}><option value="">Whole class</option>{source.data.methods.map(item => <option key={`${item.name}${item.descriptor}`} value={`${item.name}${item.descriptor}`}>{item.name}{item.descriptor}</option>)}</Select></label>
      <div className="flex flex-wrap gap-3 text-sm">{renderLink({ to: `/jvm/${encodeURIComponent(agent)}/instrument?${selection}`, children: "Watch / trace this method", className: "underline" })}<Button variant="outline" disabled={download.isPending} onClick={() => download.mutate("source")}>Download source</Button><Button variant="outline" disabled={download.isPending} onClick={() => download.mutate("bytecode")}>Download bytecode</Button><Button variant="outline" disabled={loader.startsWith("~")} onClick={() => { setDraft(source.data.source); setCompiled(undefined); setEditing(true); }}>Edit source</Button></div>
      {!editing && <CodeBlock source={displaySource} language="java" />}
      {editing && <form className="space-y-3" onSubmit={event => { event.preventDefault(); redefine.mutate(); }}>
        <p className="text-sm text-muted-foreground">Decompiled source is a reconstruction. Redefinition can change method bodies; changing fields or method signatures is rejected by the JVM. Compile the current source before applying it.</p>
        <section className="space-y-2"><h3 className="text-sm font-medium">Replacement source</h3><SourceEditor value={draft} onChange={setDraft} language="java" path={`${agent}/${className}.java`} diagnostics={compiled?.source === draft ? compiled.result.diagnostics : []} /></section>
        <label className="block text-sm">Capture duration<Select value={duration} onChange={event => setDuration(event.target.value)} options={[{ value: "2m", label: "2 minutes" }, { value: "5m", label: "5 minutes" }, { value: "15m", label: "15 minutes" }]} /></label>
        <MutationChoice label="Allow replacing class bytecode" checked={allowed} onChange={setAllowed} />
        <p className="text-sm text-muted-foreground">The capture renews its lease while recording and restores original bytes when it stops or disconnects.</p>
        <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={!draft.trim() || compile.isPending} onClick={() => compile.mutate(draft)}>Compile preview</Button><Button type="submit" disabled={!allowed || !duration.trim() || redefine.isPending || compile.isPending || !compiled?.result.success || compiled.source !== draft}>Start redefinition</Button><Button type="button" variant="outline" onClick={() => setEditing(false)}>Cancel editing</Button></div>
        {compiled && <JvmResults value={compiled.result} />}
      </form>}
      {(compile.error || redefine.error || download.error) && <p role="alert">{(compile.error ?? redefine.error ?? download.error)?.message}</p>}
      {redefine.data && <p role="status" className="text-sm">Redefinition recording. {renderLink({ to: `/?run=${encodeURIComponent(redefine.data.runId)}`, children: "Open capture to stop and restore", className: "underline" })}</p>}
    </div>}
  </Panel>;
}

function saveClassFile(name: string, blob: Blob) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
