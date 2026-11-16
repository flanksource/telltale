import { useState } from "react";
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

const surfaces = [
  ["JMX", "jvm.jmx.search"], ["Classes", "jvm.classes"], ["OGNL", "jvm.ognl.eval"], ["Groovy", "jvm.groovy.eval"],
  ["Threads", "jvm.threads"], ["Heap", "jvm.heap.histogram"], ["Metrics", "jvm.metrics"], ["Logging", "jvm.logging.list"], ["Patches", "jvm.classes.patches"], ["Extensions", "jvm.extensions.list"],
  ["Instrument", "instrument"], ["Traces", "traces"], ["Notifications", "notifications"],
];

function OperationForm({ agent, operation }: { agent: string; operation: Operation }) {
  const client = useQueryClient();
  const { navigate } = useRouter();
  const [params, setParams] = useState<Record<string, unknown>>(() => {
    const selected = selectedParameters(window.location.search);
    return Object.fromEntries(Object.entries(selected).filter(([key]) => key in (operation.paramsSchema.properties ?? {})));
  });
  const [allowMutation, setAllowMutation] = useState(false);
  const [preview, setPreview] = useState<{ params: string; success: boolean; value: unknown }>();
  const [file, setFile] = useState<File>();
  const [history, setHistory] = useState<string[]>([]);
  const [fileError, setFileError] = useState<string>();
  const patch = operation.name === "jvm.classes.patch";
  const upload = operation.name === "jvm.extensions.install" || operation.name === "jvm.extensions.replace";
  const execute = useMutation({
    mutationFn: async () => call(agent, operation.name, upload && file ? { ...params, jar: await extensionFile(file) } : params, allowMutation),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ["jvm-catalog", agent] }); void client.invalidateQueries({ queryKey: ["jvm-patches", agent] }); if (operation.name === "jvm.ognl.eval" && typeof params.expression === "string") setHistory(previous => [params.expression as string, ...previous.filter(value => value !== params.expression)].slice(0, 25)); },
  });
  const compile = useMutation({
    mutationFn: () => call(agent, "jvm.classes.compile", params),
    onSuccess: value => setPreview({ params: JSON.stringify(params), success: typeof value === "object" && value !== null && "success" in value && value.success === true, value }),
  });
  return <div className="space-y-4 min-w-0">
    <Panel title={operation.title}>
      <form className="space-y-3" onSubmit={event => { event.preventDefault(); execute.mutate(); }}>
        {operation.description && <p className="text-sm text-muted-foreground">{operation.description}</p>}
        {operation.name === "jvm.ognl.eval" && <div className="space-y-2"><p className="text-xs text-muted-foreground">Expression presets</p><div className="flex flex-wrap gap-2">{[["System properties", "@java.lang.System@getProperties()"], ["Runtime", "@java.lang.Runtime@getRuntime()"]].map(([title, expression]) => <Button key={title} type="button" variant="outline" onClick={() => setParams(current => ({ ...current, expression }))}>{title}</Button>)}</div>{history.length > 0 && <label className="block text-sm">Expression history<select className="w-full rounded border border-border bg-background px-2 py-1" value="" onChange={event => setParams(current => ({ ...current, expression: event.target.value }))}><option value="">Choose a previous expression</option>{history.map(expression => <option key={expression} value={expression}>{expression}</option>)}</select></label>}</div>}
        {operation.name === "jvm.groovy.eval" && <label className="block text-sm">Load Groovy file<input type="file" accept=".groovy,text/plain" className="block w-full" onChange={event => { const script = event.target.files?.[0]; if (!script) return; setFileError(undefined); void script.text().then(source => setParams(current => ({ ...current, source }))).catch((error: unknown) => setFileError(error instanceof Error ? error.message : String(error))); }} /></label>}
        <JsonSchemaForm schema={operation.paramsSchema} value={params} onChange={value => { setParams(value); persistParameters(value); execute.reset(); }} size="sm" idPrefix="jvm-operation" showPreferencesMenu={false} persistPreferences={false} {...(upload ? { hiddenKeys: ["jar"] } : {})} />
        {upload && <label className="block text-sm">Extension JAR<input type="file" accept=".jar,application/java-archive" required className="block w-full" onChange={event => setFile(event.target.files?.[0])} /></label>}
        {operation.mutates && <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={allowMutation} onChange={event => setAllowMutation(event.target.checked)} />Allow this operation to change the JVM</label>}
        <div className="flex flex-wrap gap-2">
          {patch && <Button type="button" variant="outline" disabled={compile.isPending} onClick={() => compile.mutate()}>Compile preview</Button>}
          <Button type="submit" disabled={execute.isPending || (operation.mutates && !allowMutation) || (upload && !file) || (patch && (!preview?.success || preview.params !== JSON.stringify(params)))}>{execute.isPending ? "Executing…" : "Execute"}</Button>
        </div>
        {(execute.error || compile.error || fileError) && <p role="alert">{fileError ?? (execute.error ?? compile.error)?.message}</p>}
        {patch && <p className="text-sm text-muted-foreground">Compile the current source before applying it. The lease restores original bytes automatically unless renewed or explicitly made permanent.</p>}
      </form>
    </Panel>
    {preview && <Panel title="Compile preview"><JvmResults value={preview.value} /></Panel>}
    {execute.data !== undefined && <Panel title="Result"><JvmResults value={execute.data} onClass={(name, loader) => navigate(`/jvm/${encodeURIComponent(agent)}/jvm.classes.source?class=${encodeURIComponent(name)}&loader=${encodeURIComponent(loader)}`)} /></Panel>}
  </div>;
}

export default function JvmPage({ agent, operation = "jvm.jmx.search" }: { agent: string; operation?: string }) {
  const { navigate, renderLink } = useRouter();
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
  return <div className="min-w-0 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3"><h1 className="text-2xl font-semibold">{operation === "instrument" ? "Instrument methods" : operation === "traces" ? "JVM traces" : operation === "notifications" ? "JMX notifications" : "JVM diagnostics"}</h1><Button size="sm" variant="outline" onClick={() => void operations.refetch()}>{dedicated ? "Refresh" : "Refresh catalog"}</Button></div>
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"><p className="break-all">Agent <code>{agent}</code></p>
    {status.data && <p role="status">Runtime mutations {status.data.allowMutation ? "enabled" : "disabled"}. {status.data.patches ? `${status.data.patches.length} active bytecode patches.` : "Instrumentation is not attached."}</p>}</div>
    {status.error && <p role="alert">Cannot inspect JVM patch status: {status.error.message}</p>}
    {!dedicated && effects}
    <nav aria-label="JVM diagnostics" className="flex flex-wrap gap-2">{surfaces.map(([title, name]) => <span key={name}>{renderLink({ to: surfaceHref({ agent, from: operation, to: name, search: window.location.search }), children: title, className: `inline-block rounded border border-border px-2 py-1 text-xs ${operation === name ? "bg-muted font-semibold" : ""}` })}</span>)}</nav>
    {dedicated && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Runtime effects and other capture types</summary><div className="mt-2 space-y-3">{effects}{captureLinks}</div></details>}
    {!dedicated && <div className="flex flex-wrap gap-3">
      <label className="text-sm">Filter operations<input className="ml-2 rounded border border-border bg-background px-2 py-1" value={search} onChange={event => { setSearch(event.target.value); persistFilter("opFilter", event.target.value); }} /></label>
      <label className="min-w-0 text-sm">Operation<select className="ml-2 max-w-full rounded border border-border bg-background px-2 py-1" value={operation} onChange={event => navigate(`/jvm/${encodeURIComponent(agent)}/${encodeURIComponent(event.target.value)}${window.location.search}`)}>
        {operations.data?.filter(item => item.name === operation || `${item.name} ${item.title}`.toLowerCase().includes(search.toLowerCase())).map(item => <option key={item.name} value={item.name}>{item.title}</option>)}
      </select></label>
    </div>}
    {!dedicated && captureLinks}
    {operations.isPending && <p role="status">Loading diagnostic operations…</p>}
    {operations.error && <p role="alert">{operations.error.message}</p>}
    {operations.data && !selected && !dedicated && <p role="alert">Operation {operation} is not available. Refresh the catalog after installing or unloading an extension.</p>}
    {dedicated ?? (selected && <OperationSurface key={`${agent}:${operation}`} agent={agent} operation={selected} />)}
  </div>;
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
