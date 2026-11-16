import { useContext, useState, useSyncExternalStore } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, JsonSchemaForm, Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { call, catalog, extensionFile, persistFilter, persistParameters, selectedParameters, type Operation } from "./api";
import { JvmResults } from "./JvmResults";
import { JvmMBeans } from "./JvmMBeans";
import { JvmClasses } from "./JvmClasses";
import { JvmLogging, JvmPatches, JvmThreads } from "./JvmInspectors";
import { JvmMetrics } from "./JvmMetrics";
import { JvmHeapHistogram } from "./JvmHeapHistogram";
import { JvmInstrument } from "./JvmInstrument";
import { JvmTraces } from "./JvmTraces";
import { JvmNotifications } from "./JvmNotifications";
import { surfaceHref } from "./captures";
import { JvmRuntime } from "./runtime";
import { SourceEditor } from "./SourceEditor";
import { Select, Checkbox, InputField } from "@flanksource/clicky-ui/components";
import type { Compilation } from "./diagnostics";

const surfaces = [
  ["JMX", "jvm.jmx.search"], ["Classes", "jvm.classes"], ["OGNL", "jvm.ognl.eval"], ["Groovy", "jvm.groovy.eval"],
  ["Threads", "jvm.threads"], ["Heap", "jvm.heap.histogram"], ["Metrics", "jvm.metrics"], ["Logging", "jvm.logging.list"], ["Patches", "jvm.classes.patches"], ["Extensions", "jvm.extensions.list"],
  ["Instrument", "instrument"], ["Traces", "traces"], ["Notifications", "notifications"],
];

function OperationForm({ agent, operation }: { agent: string; operation: Operation }) {
  const runtime = useContext(JvmRuntime);
  const client = useQueryClient();
  const { navigate } = useRouter();
  const [params, setParams] = useState<Record<string, unknown>>(() => {
    const selected = selectedParameters(window.location.search);
    return { ...(operation.name === "jvm.classes.patch" ? { leaseSeconds: 120 } : {}), ...Object.fromEntries(Object.entries(selected).filter(([key]) => key in (operation.paramsSchema.properties ?? {}))) };
  });
  const [allowMutation, setAllowMutation] = useState(false);
  const [preview, setPreview] = useState<{ params: string; success: boolean; value: Compilation }>();
  const [file, setFile] = useState<File>();
  const historyKey = `telltale.jvm.${agent}.${operation.name}.history`;
  const [history, setHistory] = useState<string[]>(() => { const saved = localStorage.getItem(historyKey); if (!saved) return []; const value: unknown = JSON.parse(saved); if (!Array.isArray(value) || value.some(item => typeof item !== "string")) throw new Error("Invalid JVM expression history"); return value as string[]; });
  const [fileError, setFileError] = useState<string>();
  const patch = operation.name === "jvm.classes.patch";
  const compileParams = Object.fromEntries(Object.entries(params).filter(([key]) => key !== "leaseSeconds"));
  const compileIdentity = JSON.stringify(compileParams);
  const upload = operation.name === "jvm.extensions.install" || operation.name === "jvm.extensions.replace";
  const execute = useMutation({
    mutationFn: async (submitted: { params: Record<string, unknown>; file?: File; allowMutation: boolean }) => call(agent, operation.name, upload && submitted.file ? { ...submitted.params, jar: await extensionFile(submitted.file) } : submitted.params, submitted.allowMutation),
    onSuccess: (_value, submitted) => { void client.invalidateQueries({ queryKey: ["jvm-catalog", agent] }); void client.invalidateQueries({ queryKey: ["jvm-patches", agent] }); if (operation.name === "jvm.ognl.eval" && typeof submitted.params.expression === "string") { const next = [submitted.params.expression, ...history.filter(value => value !== submitted.params.expression)].slice(0, 25); localStorage.setItem(historyKey, JSON.stringify(next)); setHistory(next); } },
  });
  const compile = useMutation({
    mutationFn: async (submitted: Record<string, unknown>) => {
      const value = await call(agent, "jvm.classes.compile", submitted);
      if (!value || typeof value !== "object" || !("success" in value) || typeof value.success !== "boolean" || !("diagnostics" in value) || !Array.isArray(value.diagnostics)) throw new Error("Invalid JVM compilation response");
      return value as Compilation;
    },
    onSuccess: (value, submitted) => setPreview({ params: JSON.stringify(submitted), success: value.success, value }),
  });
  return <div className="space-y-4 min-w-0">
    <Panel title={operation.title}>
      <form className="space-y-3" onSubmit={event => { event.preventDefault(); execute.mutate({ params, file, allowMutation }); }}>
        {operation.description && <p className="text-sm text-muted-foreground">{operation.description}</p>}
        {operation.name === "jvm.ognl.eval" && <div className="space-y-2"><p className="text-xs text-muted-foreground">Expression presets</p><div className="flex flex-wrap gap-2">{[["System properties", "@java.lang.System@getProperties()"], ["Runtime", "@java.lang.Runtime@getRuntime()"]].map(([title, expression]) => <Button key={title} type="button" variant="outline" onClick={() => setParams(current => ({ ...current, expression }))}>{title}</Button>)}</div>{history.length > 0 && <label className="block text-sm">Expression history<Select className="w-full rounded border border-border bg-background px-2 py-1" value="" onChange={event => setParams(current => ({ ...current, expression: event.target.value }))}><option value="">Choose a previous expression</option>{history.map(expression => <option key={expression} value={expression}>{expression}</option>)}</Select></label>}</div>}
        {operation.name === "jvm.groovy.eval" && <label className="block text-sm">Load Groovy file<input type="file" accept=".groovy,text/plain" className="block w-full" onChange={event => { const script = event.target.files?.[0]; if (!script) return; setFileError(undefined); void script.text().then(source => setParams(current => ({ ...current, source }))).catch((error: unknown) => setFileError(error instanceof Error ? error.message : String(error))); }} /></label>}
        <JsonSchemaForm schema={operation.paramsSchema} value={params} onChange={value => { setParams(value); persistParameters(value); execute.reset(); }} size="sm" idPrefix="jvm-operation" showPreferencesMenu={false} persistPreferences={false} hiddenKeys={[...(upload ? ["jar"] : []), ...(patch ? ["leaseSeconds"] : []), ...("source" in (operation.paramsSchema.properties ?? {}) ? ["source"] : [])]} />
        {"source" in (operation.paramsSchema.properties ?? {}) && <section className="space-y-2"><h3 className="text-sm font-medium">Source (required)</h3><SourceEditor value={String(params.source ?? "")} onChange={source => { setParams(current => ({ ...current, source })); execute.reset(); }} language={operation.name === "jvm.groovy.eval" ? "groovy" : "java"} path={`${agent}/${operation.name}`} diagnostics={preview?.params === compileIdentity ? preview.value.diagnostics : []} /></section>}
        {patch && <label className="block text-sm">Restore original bytecode after<Select value={String(params.leaseSeconds)} onChange={event => setParams(current => ({ ...current, leaseSeconds: Number(event.target.value) }))} options={[{ value: "120", label: "2 minutes" }, { value: "300", label: "5 minutes" }, { value: "900", label: "15 minutes" }]} /></label>}
        {upload && <label className="block text-sm">Extension JAR<input type="file" accept=".jar,application/java-archive" required className="block w-full" onChange={event => setFile(event.target.files?.[0])} /></label>}
        {operation.mutates && <label className="flex items-start gap-2 text-sm"><Checkbox checked={allowMutation} disabled={runtime !== true} onChange={setAllowMutation} />Allow this operation to change the JVM</label>}
        {operation.mutates && runtime !== true && <p role="status" className="text-sm">{runtime === undefined ? "Checking runtime capabilities…" : "Runtime mutations are disabled for this agent. Enable them in the agent configuration before running this operation. Your draft is retained."}</p>}
        <div className="flex flex-wrap gap-2">
          {patch && <Button type="button" variant="outline" disabled={compile.isPending || !params.class || !String(params.source ?? "").trim()} onClick={() => compile.mutate(compileParams)}>Compile preview</Button>}
          <Button type="submit" disabled={execute.isPending || (operation.mutates && (!allowMutation || runtime !== true)) || ("source" in (operation.paramsSchema.properties ?? {}) && !String(params.source ?? "").trim()) || (upload && !file) || (patch && (!preview?.success || preview.params !== compileIdentity))}>{execute.isPending ? "Executing…" : patch ? "Apply patch" : operation.name === "jvm.groovy.eval" ? "Run script" : operation.name === "jvm.ognl.eval" ? "Evaluate expression" : operation.title}</Button>
        </div>
        {(execute.error || compile.error || fileError) && <p role="alert">{fileError ?? (execute.error ?? compile.error)?.message}</p>}
        {patch && <p className="text-sm text-muted-foreground">Compile the current source before applying it. Original bytecode is restored automatically after the selected duration.</p>}
      </form>
    </Panel>
    {preview && <Panel title="Compile preview"><JvmResults value={preview.value} /></Panel>}
    {execute.data !== undefined && <Panel title="Result"><JvmResults value={execute.data} onClass={(name, loader) => navigate(`/jvm/${encodeURIComponent(agent)}/jvm.classes.source?class=${encodeURIComponent(name)}&loader=${encodeURIComponent(loader)}`)} /></Panel>}
  </div>;
}

export default function JvmPage({ agent, operation = "jvm.jmx.search" }: { agent: string; operation?: string }) {
  const { navigate, renderLink } = useRouter();
  useSyncExternalStore(listener => { window.addEventListener("popstate", listener); return () => window.removeEventListener("popstate", listener); }, () => window.location.search);
  const operations = useQuery({ queryKey: ["jvm-catalog", agent], queryFn: () => catalog(agent), retry: false });
  const status = useQuery({ queryKey: ["jvm-patches", agent], queryFn: async () => {
    const value = await call(agent, "jvm.status", {});
    if (!value || typeof value !== "object" || !("allowMutation" in value) || typeof value.allowMutation !== "boolean") throw new Error("Invalid JVM status response");
    return { allowMutation: value.allowMutation, patches: "patches" in value && Array.isArray(value.patches) ? value.patches : undefined };
  }, retry: false, refetchInterval: 10000 });
  const [search, setSearch] = useState(() => new URLSearchParams(window.location.search).get("opFilter") ?? "");
  const selected = operations.data?.find(item => item.name === operation);
  const dedicated = operation === "instrument" ? <JvmInstrument agent={agent} /> : operation === "traces" ? <JvmTraces agent={agent} /> : operation === "notifications" ? <JvmNotifications agent={agent} /> : undefined;
  const effects = <p className="rounded border border-border p-3 text-sm">Diagnostics operate across the entire JVM. Patches, logging changes and scripts can affect every application sharing it. {renderLink({ to: `/jvm/${encodeURIComponent(agent)}/jvm.classes.patches`, children: "Inspect active patches", className: "underline" })}</p>;
  const captureLinks = <div className="flex flex-wrap gap-3 text-sm">{[["JFR / profiler", "jvm_jfr"], ["Watch / trace methods", "jvm_trace"], ["Redefine", "jvm_redefine"], ["Notifications / GC", "jvm_notifications"], ["Periodic OGNL", "jvm_ognl"]].map(([title, kind]) => <span key={kind}>{renderLink({ to: `/arm/${encodeURIComponent(agent)}/${kind}${window.location.search}`, children: title, className: "underline" })}</span>)}</div>;
  return <JvmRuntime.Provider value={status.data?.allowMutation}><div className="flex h-full min-h-0 min-w-0 flex-col gap-3 overflow-auto">
    <div className="flex flex-wrap items-center justify-between gap-3"><h1 className="text-2xl font-semibold">{operation === "instrument" ? "Instrument methods" : operation === "traces" ? "JVM traces" : operation === "notifications" ? "JMX notifications" : "JVM diagnostics"}</h1><Button size="sm" variant="outline" onClick={() => void operations.refetch()}>{dedicated ? "Refresh" : "Refresh catalog"}</Button></div>
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"><p className="break-all">Agent <code>{agent}</code></p>
    {status.data && <p role="status">Runtime mutations {status.data.allowMutation ? "enabled" : "disabled"}. {status.data.patches ? `${status.data.patches.length} active bytecode patches.` : "Instrumentation is not attached."}</p>}</div>
    {status.error && <p role="alert">Cannot inspect JVM patch status: {status.error.message}</p>}
    {!dedicated && effects}
    <nav aria-label="JVM diagnostics" className="flex flex-wrap gap-2">{surfaces.map(([title, name]) => <span key={name}>{renderLink({ to: surfaceHref({ agent, from: operation, to: name, search: window.location.search }), children: title, className: `inline-block rounded border border-border px-2 py-1 text-xs ${operation === name ? "bg-muted font-semibold" : ""}` })}</span>)}</nav>
    {dedicated && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Runtime effects and other capture types</summary><div className="mt-2 space-y-3">{effects}{captureLinks}</div></details>}
    {!dedicated && <details><summary className="cursor-pointer text-sm">Advanced operations and capture types</summary><div className="flex flex-wrap gap-3 py-2">
      <label className="text-sm">Filter operations<InputField value={search} onChange={value => { setSearch(value); persistFilter("opFilter", value); }} /></label>
      <label className="min-w-0 text-sm">Operation<Select className="ml-2 max-w-full rounded border border-border bg-background px-2 py-1" value={operation} onChange={event => navigate(`/jvm/${encodeURIComponent(agent)}/${encodeURIComponent(event.target.value)}${window.location.search}`)}>
        {operations.data?.filter(item => item.name === operation || `${item.name} ${item.title}`.toLowerCase().includes(search.toLowerCase())).map(item => <option key={item.name} value={item.name}>{item.title}</option>)}
      </Select></label>
    </div>{captureLinks}</details>}
    {operations.isPending && <p role="status">Loading diagnostic operations…</p>}
    {operations.error && <p role="alert">{operations.error.message}</p>}
    {operations.data && !selected && !dedicated && <p role="alert">Operation {operation} is not available. Refresh the catalog after installing or unloading an extension.</p>}
    <div className="min-h-0 flex-1">{dedicated ?? (selected && <OperationSurface key={`${agent}:${operation}`} agent={agent} operation={selected} />)}</div>
  </div></JvmRuntime.Provider>;
}

function OperationSurface({ agent, operation }: { agent: string; operation: Operation }) {
  const guided = operation.name === "jvm.jmx.search" ? <JvmMBeans agent={agent} />
    : ["jvm.classes", "jvm.classes.source"].includes(operation.name) ? <JvmClasses agent={agent} />
    : operation.name === "jvm.logging.list" ? <JvmLogging agent={agent} />
    : operation.name === "jvm.classes.patches" ? <JvmPatches agent={agent} />
    : operation.name === "jvm.metrics" ? <JvmMetrics agent={agent} />
    : operation.name === "jvm.threads" ? <JvmThreads agent={agent} />
    : operation.name === "jvm.heap.histogram" ? <JvmHeapHistogram agent={agent} /> : undefined;
  return guided ? <div className="space-y-4">{guided}<details><summary className="cursor-pointer text-sm">Advanced operation</summary><div className="mt-3"><OperationForm agent={agent} operation={operation} /></div></details></div> : <OperationForm agent={agent} operation={operation} />;
}
