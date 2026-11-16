import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Panel, Tabs, Checkbox, InputField } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { call, persistFilter } from "./api";
import { inspect, type MBeanAttribute, type MBeanInfo, type MBeanOperation } from "./diagnostics";
import { buildMBeanBrowserTree, parseMBeanArgument } from "./mbeans";
import { JvmResults } from "./JvmResults";
import { MutationChoice } from "./MutationChoice";

export function JvmMBeans({ agent }: { agent: string }) {
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("mbeanFilter") ?? "");
  const [selected, setSelected] = useState(() => new URLSearchParams(window.location.search).get("objectName") ?? "");
  const names = useQuery({ queryKey: ["jvm-mbeans", agent], queryFn: () => inspect<string[]>(agent, "jvm.jmx.search"), staleTime: 30000 });
  const tree = buildMBeanBrowserTree((names.data ?? []).filter(name => name.toLowerCase().includes(filter.toLowerCase())));
  return <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(220px,1fr)_minmax(0,3fr)]">
    <Panel title="MBeans">
      <label className="block text-sm">Filter MBeans<InputField value={filter} onChange={value => { setFilter(value); persistFilter("mbeanFilter", value); }} /></label>
      <p className="my-2 text-xs text-muted-foreground">{tree.reduce((total, domain) => total + domain.count, 0)} of {names.data?.length ?? 0} MBeans</p>
      {names.isPending && <p role="status">Loading MBeans…</p>}
      {names.error && <p role="alert">{names.error.message}</p>}
      <div className="max-h-[36rem] overflow-auto">{tree.map(domain => <details key={domain.domain} open>
        <summary className="py-1 text-sm font-medium">{domain.domain} ({domain.count})</summary>
        {domain.types.map(type => <details key={type.type} open className="ml-3 border-l border-border pl-2"><summary className="py-1 text-sm">{type.type} ({type.mbeans.length})</summary>
          {type.mbeans.map(bean => <button type="button" key={bean.objectName} aria-label={bean.objectName} aria-pressed={selected === bean.objectName} title={bean.objectName} className={`block w-full min-w-0 rounded px-2 py-1 text-left text-sm hover:bg-muted ${selected === bean.objectName ? "bg-muted" : ""}`} onClick={() => { setSelected(bean.objectName); persistFilter("objectName", bean.objectName); }}><span className="block break-words font-mono">{bean.label}</span>{bean.metadata && <span className="block break-words text-xs text-muted-foreground">{bean.metadata}</span>}</button>)}
        </details>)}
      </details>)}</div>
      {!names.isPending && !names.error && tree.length === 0 && <p className="py-3 text-sm">No matching MBeans.</p>}
    </Panel>
    {selected ? <MBeanDetail key={`${agent}:${selected}`} agent={agent} objectName={selected} /> : <Panel title="MBean details"><p className="text-sm text-muted-foreground">Select an MBean to inspect metadata, live attributes and operations.</p></Panel>}
  </div>;
}

function MBeanDetail({ agent, objectName }: { agent: string; objectName: string }) {
  const { renderLink } = useRouter();
  const detail = useQuery({ queryKey: ["jvm-mbean", agent, objectName], queryFn: () => inspect<MBeanInfo>(agent, "jvm.jmx.info", { objectName }) });
  const values = useQuery({ queryKey: ["jvm-mbean-values", agent, objectName], queryFn: () => inspect<{ attributes: Array<{ name: string; value: unknown }> }>(agent, "jvm.jmx.read", { objectName }) });
  const [view, setView] = useState("attributes");
  const [allowMutation, setAllowMutation] = useState(false);
  return <Panel title={objectName}>
    {detail.isPending && <p role="status">Reading MBean metadata…</p>}
    {detail.error && <p role="alert">{detail.error.message}</p>}
    {detail.data && <div className="min-w-0 space-y-3">
      <p className="break-all font-mono text-xs">{detail.data.className}</p>
      {detail.data.description && <p className="text-sm text-muted-foreground">{detail.data.description}</p>}
      <MutationChoice label="Allow MBean changes" checked={allowMutation} onChange={setAllowMutation} />
      <Tabs tabs={[{ id: "attributes", label: "Attributes", count: detail.data.attributes.length }, { id: "operations", label: "Operations", count: detail.data.operations.length }, { id: "notifications", label: "Notifications", count: detail.data.notifications?.length ?? 0 }]} value={view} onChange={setView} />
      {view === "attributes" && <div className="space-y-3"><Button variant="outline" disabled={values.isFetching} onClick={() => void values.refetch()}>Refresh values</Button>{values.error && <p role="alert">{values.error.message}</p>}{detail.data.attributes.map(attribute => <Attribute key={attribute.name} agent={agent} objectName={objectName} attribute={attribute} value={values.data?.attributes.find(item => item.name === attribute.name)?.value} allowMutation={allowMutation} />)}</div>}
      {view === "operations" && <div className="space-y-4">{detail.data.operations.length === 0 && <p className="text-sm">No operations.</p>}{detail.data.operations.map(operation => <MBeanInvoke key={`${operation.name}(${operation.signature.map(arg => arg.type).join(",")})`} agent={agent} objectName={objectName} operation={operation} allowMutation={allowMutation} />)}</div>}
      {view === "notifications" && <div className="space-y-2">{renderLink({ to: `/jvm/${encodeURIComponent(agent)}/notifications?${new URLSearchParams({ objectName })}`, children: "Record notifications from this MBean", className: "text-sm underline" })}<JvmResults value={detail.data.notifications ?? []} /></div>}
    </div>}
  </Panel>;
}

function Attribute({ agent, objectName, attribute, value, allowMutation }: { agent: string; objectName: string; attribute: MBeanAttribute; value: unknown; allowMutation: boolean }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const write = useMutation({ mutationFn: () => call(agent, "jvm.jmx.write", { objectName, attribute: attribute.name, value: parseMBeanArgument(draft, attribute.type) }, allowMutation), onSuccess: () => { setEditing(false); void client.invalidateQueries({ queryKey: ["jvm-mbean-values", agent, objectName] }); } });
  return <section className="min-w-0 space-y-2 rounded border border-border p-3">
    <div className="flex flex-wrap items-center gap-2"><h3 className="font-mono text-sm font-medium">{attribute.name}</h3><span className="text-xs text-muted-foreground">{attribute.type} · {attribute.writable ? "writable" : "read only"}</span>{attribute.writable && <Button variant="outline" onClick={() => { setDraft(typeof value === "object" ? JSON.stringify(value) : String(value ?? "")); setEditing(!editing); }}>Edit {attribute.name}</Button>}</div>
    {attribute.description && <p className="text-xs text-muted-foreground">{attribute.description}</p>}
    {attribute.readable && (value === undefined ? <p className="text-xs text-muted-foreground">Value unavailable.</p> : <JvmResults value={value} />)}
    {editing && <form className="space-y-2" onSubmit={event => { event.preventDefault(); write.mutate(); }}><label className="block text-sm">New {attribute.name} ({attribute.type}){attribute.type === "boolean" || attribute.type === "java.lang.Boolean" ? <Checkbox checked={draft === "true"} onChange={value => setDraft(String(value))} /> : <InputField value={draft} onChange={setDraft} />}</label><Button type="submit" disabled={!allowMutation || write.isPending}>Write {attribute.name}</Button></form>}
    {write.error && <p role="alert">{write.error.message}</p>}
  </section>;
}

function MBeanInvoke({ agent, objectName, operation, allowMutation }: { agent: string; objectName: string; operation: MBeanOperation; allowMutation: boolean }) {
  const [args, setArgs] = useState<string[]>(operation.signature.map(arg => ["boolean", "java.lang.Boolean"].includes(arg.type) ? "false" : ""));
  const client = useQueryClient();
  const invoke = useMutation({ mutationFn: () => call(agent, "jvm.jmx.invoke", { objectName, operation: operation.name, args: args.map((value, index) => parseMBeanArgument(value, operation.signature[index].type)), signature: operation.signature.map(arg => arg.type) }, allowMutation), onSuccess: () => { void client.invalidateQueries({ queryKey: ["jvm-mbean-values", agent, objectName] }); } });
  return <form className="min-w-0 space-y-2 rounded border border-border p-3" onSubmit={event => { event.preventDefault(); invoke.mutate(); }}>
    <h3 className="break-all font-mono text-sm font-medium">{operation.name}({operation.signature.map(arg => arg.type).join(", ")}) → {operation.returnType}</h3>
    {operation.description && <p className="text-xs text-muted-foreground">{operation.description}</p>}
    {operation.signature.map((arg, index) => <label key={`${arg.name}:${index}`} className="block text-sm">{arg.name} ({arg.type}){["boolean", "java.lang.Boolean"].includes(arg.type) ? <Checkbox checked={args[index] === "true"} onChange={value => setArgs(current => current.map((other, i) => i === index ? String(value) : other))} /> : <InputField value={args[index]} onChange={value => setArgs(current => current.map((other, i) => i === index ? value : other))} />}</label>)}
    <Button type="submit" disabled={!allowMutation || invoke.isPending}>Invoke {operation.name}</Button>
    {invoke.error && <p role="alert">{invoke.error.message}</p>}
    {invoke.isSuccess && <JvmResults value={invoke.data} />}
  </form>;
}
