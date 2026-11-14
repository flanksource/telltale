import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Combobox } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fieldClass, inspect, jvmHref, type ClassLoader, type ClassPage } from "./diagnostics";
import type { MethodTarget } from "./captures";

export function JvmMethodTargets({ agent, targets, onChange }: { agent: string; targets: MethodTarget[]; onChange: (targets: MethodTarget[]) => void }) {
  const loaders = useQuery({ queryKey: ["jvm-loaders", agent], queryFn: () => inspect<ClassLoader[]>(agent, "jvm.loaders") });
  return <div className="space-y-3">
    {targets.map((row, index) => <TargetFields key={index} agent={agent} row={row} index={index} loaders={loaders.data ?? []} change={next => onChange(targets.map((other, at) => at === index ? next : other))} remove={() => onChange(targets.filter((_, at) => at !== index))} removable={targets.length > 1} />)}
    {loaders.error && <p role="alert" className="text-sm">{loaders.error.message}</p>}
    <div className="flex flex-wrap items-center gap-3"><Button type="button" variant="outline" disabled={targets.length >= 8} onClick={() => onChange([...targets, { class: "", method: "", loader: "" }])}>Add method</Button><p className="text-xs text-muted-foreground">Up to eight methods share one event budget and call tree.</p></div>
  </div>;
}

function TargetFields({ agent, row, index, loaders, change, remove, removable }: { agent: string; row: MethodTarget; index: number; loaders: ClassLoader[]; change: (row: MethodTarget) => void; remove: () => void; removable: boolean }) {
  const { renderLink } = useRouter();
  const [query, setQuery] = useState(row.class);
  const classes = useQuery({ queryKey: ["jvm-method-classes", agent, query, row.loader], queryFn: () => inspect<ClassPage>(agent, "jvm.classes", { query, ...(row.loader ? { loader: row.loader } : {}), includePlatform: true, limit: 100 }), enabled: !!query.trim() });
  const definitions = classes.data?.classes.filter(item => item.name === row.class) ?? [];
  return <fieldset className="min-w-0 space-y-2 rounded border p-3"><legend className="px-1 text-xs font-medium">Method {index + 1}</legend>
    <div className="flex flex-wrap items-end gap-3"><div className="min-w-0 flex-1"><label htmlFor={`capture-class-${index}`} className="mb-1 block text-sm">Class</label><Combobox id={`capture-class-${index}`} ariaLabel={`Class ${index + 1}`} value={row.class} onChange={value => change({ ...row, class: value, method: "" })} onSearch={setQuery} options={[...new Set(classes.data?.classes.map(item => item.name))].map(name => ({ value: name, label: name }))} loading={classes.isFetching} /></div><Button type="button" variant="ghost" disabled={!removable} onClick={remove}>Remove method {index + 1}</Button></div>
    <label className="block text-sm">Method selector<input required className={`${fieldClass} font-mono`} value={row.method} onChange={event => change({ ...row, method: event.target.value })} placeholder="compute(J)D or compute*" /></label>
    <label className="block text-sm">Class loader<select className={fieldClass} value={row.loader} onChange={event => change({ ...row, loader: event.target.value })}><option value="">Resolve unique definition</option>{loaders.filter(loader => !loader.virtual).map(loader => <option key={loader.hash} value={loader.hash}>{loader.label} · {loader.hash}</option>)}{row.loader && !loaders.some(loader => loader.hash === row.loader) && <option value={row.loader}>{row.loader}</option>}</select></label>
    {definitions.length > 1 && !row.loader && <p role="alert" className="text-sm">Several loaders define this class. Select a concrete loader before instrumenting.</p>}
    {classes.error && <p role="alert" className="text-sm">{classes.error.message}</p>}
    {row.class && renderLink({ to: jvmHref(agent, "jvm.classes.source", { class: row.class, loader: row.loader, method: row.method }), children: "Browse source and overloads", className: "text-xs underline" })}
  </fieldset>;
}
