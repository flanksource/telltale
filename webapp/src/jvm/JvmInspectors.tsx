import { Select, InputField } from "@flanksource/clicky-ui/components";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { CodeBlock, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { call, persistFilter } from "./api";
import { fieldClass, inspect, jvmHref, type LoggerRow, type PatchRow, type ProbeOwner, type Threads, type ThreadRow, type ThreadStack } from "./diagnostics";
import { MutationChoice } from "./MutationChoice";

const levels = ["OFF", "SEVERE", "WARNING", "INFO", "CONFIG", "FINE", "FINER", "FINEST", "ALL"];

export function JvmLogging({ agent }: { agent: string }) {
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("loggerFilter") ?? "");
  const [editing, setEditing] = useState<LoggerRow>();
  const loggers = useQuery({ queryKey: ["jvm-loggers", agent], queryFn: () => inspect<LoggerRow[]>(agent, "jvm.logging.list") });
  const columns: DataTableColumn<LoggerRow>[] = [
    { key: "name", label: "Logger", render: (_, row) => <span className="break-all font-mono">{row.name || "<root>"}</span> },
    { key: "level", label: "Level", render: (_, row) => row.level ?? "(inherit)" },
    { key: "parent", label: "Parent", render: (_, row) => row.parent === null ? "—" : row.parent || "<root>" },
    { key: "actions", label: "Actions", render: (_, row) => <Button variant="outline" aria-label={`Edit ${row.name || "root logger"}`} onClick={() => setEditing(row)}>Edit</Button> },
  ];
  return <Panel title="Java logging">
    <div className="space-y-3 min-w-0"><p className="text-sm text-muted-foreground">java.util.logging loggers. Inherit uses the parent’s level.</p>
      <div className="flex flex-wrap gap-3"><label className="text-sm">Filter loggers<InputField value={filter} onChange={value => { setFilter(value); persistFilter("loggerFilter", value); }} /></label><Button variant="outline" disabled={loggers.isFetching} onClick={() => void loggers.refetch()}>Refresh loggers</Button></div>
      {loggers.error && <p role="alert">{loggers.error.message}</p>}
      <div className="flex h-80 min-h-0 min-w-0 flex-col"><DataTable data={(loggers.data ?? []).filter(row => row.name.toLowerCase().includes(filter.toLowerCase()))} columns={columns} loading={loggers.isPending} getRowId={row => row.name} emptyMessage="No matching loggers" /></div>
      {editing && <EditLogger key={editing.name} agent={agent} row={editing} close={() => setEditing(undefined)} />}
    </div>
  </Panel>;
}

function EditLogger({ agent, row, close }: { agent: string; row: LoggerRow; close: () => void }) {
  const client = useQueryClient();
  const [level, setLevel] = useState(row.level ?? "inherit");
  const [allowed, setAllowed] = useState(false);
  const update = useMutation({ mutationFn: () => call(agent, "jvm.logging.set", { name: row.name, level: level === "inherit" ? null : level }, allowed), onSuccess: () => { void client.invalidateQueries({ queryKey: ["jvm-loggers", agent] }); close(); } });
  return <form className="space-y-3 rounded border border-border p-3" onSubmit={event => { event.preventDefault(); update.mutate(); }}>
    <h3 className="break-all font-mono text-sm">Set level for {row.name || "<root>"}</h3>
    <label className="block text-sm">New level<Select className={fieldClass} value={level} onChange={event => setLevel(event.target.value)}><option value="inherit">(inherit)</option>{[...new Set([...levels, ...(row.level ? [row.level] : [])])].map(value => <option key={value}>{value}</option>)}</Select></label>
    <MutationChoice label="Allow logging changes" checked={allowed} onChange={setAllowed} />
    <div className="flex gap-2"><Button type="submit" disabled={!allowed || update.isPending}>Apply level</Button><Button variant="outline" type="button" onClick={close}>Cancel</Button></div>
    {update.error && <p role="alert">{update.error.message}</p>}
  </form>;
}

export function JvmThreads({ agent }: { agent: string }) {
  const [paused, setPaused] = useState(false);
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("threadFilter") ?? "");
  const [state, setState] = useState(() => new URLSearchParams(window.location.search).get("threadState") ?? "");
  const [selected, setSelected] = useState(() => new URLSearchParams(window.location.search).get("thread") ?? "");
  const threads = useQuery({ queryKey: ["jvm-threads", agent], queryFn: () => inspect<Threads>(agent, "jvm.threads", { sampleMs: 200 }), refetchInterval: paused ? false : 5000, refetchIntervalInBackground: false });
  const select = (id: number) => { setSelected(String(id)); persistFilter("thread", String(id)); };
  const columns: DataTableColumn<ThreadRow>[] = [
    { key: "id", label: "ID" }, { key: "name", label: "Thread", minWidth: 180 }, { key: "state", label: "State" },
    { key: "cpuPercent", label: "CPU %", render: (_, row) => row.cpuPercent === null ? "unavailable" : `${row.cpuPercent}%` },
    { key: "lockOwnerName", label: "Lock owner", render: (_, row) => row.lockOwnerId === null ? "—" : <Button variant="ghost" onClick={event => { event.stopPropagation(); select(row.lockOwnerId!); }}>{row.lockOwnerName ?? `#${row.lockOwnerId}`}</Button> },
  ];
  return <div className="space-y-4">
    <Panel title="Threads"><div className="space-y-3 min-w-0">
      {threads.data && <p className="text-sm">{threads.data.threads.length} threads · CPU sampled over {threads.data.sampledMs} ms{!threads.data.cpuTimeEnabled && " · CPU time is disabled in this JVM"}</p>}
      {threads.data && threads.data.deadlocked.length > 0 && <div role="alert" className="rounded border border-destructive p-3 text-sm">Deadlock detected: {threads.data.deadlocked.map(id => <Button key={id} variant="outline" onClick={() => select(id)}>Thread {id}</Button>)}</div>}
      <div className="flex flex-wrap items-end gap-3"><label className="text-sm">Filter threads<InputField value={filter} onChange={value => { setFilter(value); persistFilter("threadFilter", value); }} /></label>
        <label className="text-sm">Thread state<Select className={fieldClass} value={state} onChange={event => { setState(event.target.value); persistFilter("threadState", event.target.value); }}><option value="">All states</option>{[...new Set(threads.data?.threads.map(row => row.state))].sort().map(value => <option key={value}>{value}</option>)}</Select></label>
        <Button variant="outline" onClick={() => setPaused(!paused)}>{paused ? "Resume polling" : "Pause polling"}</Button><Button variant="outline" disabled={threads.isFetching} onClick={() => void threads.refetch()}>Refresh threads</Button>
      </div>
      {threads.error && <p role="alert">{threads.error.message}</p>}
      <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={(threads.data?.threads ?? []).filter(row => (!state || row.state === state) && `${row.name} ${row.id} ${row.lockName ?? ""}`.toLowerCase().includes(filter.toLowerCase()))} columns={columns} getRowId={row => String(row.id)} loading={threads.isPending} defaultSort={{ key: "cpuPercent", dir: "desc" }} onRowClick={row => select(row.id)} emptyMessage="No matching threads" /></div>
    </div></Panel>
    {selected && <ThreadDetail key={`${agent}:${selected}`} agent={agent} id={Number(selected)} />}
  </div>;
}

function ThreadDetail({ agent, id }: { agent: string; id: number }) {
  const { renderLink } = useRouter();
  const stack = useQuery({ queryKey: ["jvm-thread-stack", agent, id], queryFn: () => inspect<ThreadStack>(agent, "jvm.threads.stack", { id }) });
  return <Panel title={`Thread ${id}`}>
    {stack.isPending && <p role="status">Reading stack…</p>}{stack.error && <p role="alert">{stack.error.message}</p>}
    {stack.data && <div className="space-y-3"><p className="text-sm">{stack.data.thread.name} · {stack.data.thread.state}</p><CodeBlock source={stack.data.text} language="java" /><details><summary className="text-sm">Inspect frame source</summary><ul className="space-y-1 py-2">{stack.data.frames.map((frame, index) => <li key={index} className="break-all font-mono text-xs">{renderLink({ to: jvmHref(agent, "jvm.classes.source", { class: frame.className, method: frame.methodName }), children: `${frame.className}.${frame.methodName}:${frame.lineNumber}`, className: "underline" })}</li>)}</ul></details><Button variant="outline" disabled={stack.isFetching} onClick={() => void stack.refetch()}>Refresh stack</Button></div>}
  </Panel>;
}

export function JvmPatches({ agent }: { agent: string }) {
  const client = useQueryClient();
  const { renderLink } = useRouter();
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("patchFilter") ?? "");
  const [allowed, setAllowed] = useState(false);
  const patches = useQuery({ queryKey: ["jvm-patch-register", agent], queryFn: () => inspect<PatchRow[]>(agent, "jvm.classes.patches"), refetchInterval: 15000 });
  const probes = useQuery({ queryKey: ["jvm-probe-owners", agent], queryFn: () => inspect<{ keys: ProbeOwner[] }>(agent, "jvm.probes.list"), refetchInterval: 15000 });
  const change = useMutation({ mutationFn: ({ row, operation }: { row: PatchRow; operation: "restore" | "lease" }) => call(agent, `jvm.classes.${operation}`, { class: row.class, loader: row.loader, ...(operation === "lease" ? { seconds: 120 } : {}) }, allowed), onSettled: () => { void client.invalidateQueries({ queryKey: ["jvm-patch-register", agent] }); void client.invalidateQueries({ queryKey: ["jvm-patches", agent] }); void client.invalidateQueries({ queryKey: ["jvm-class-source", agent] }); void client.invalidateQueries({ queryKey: ["jvm-probe-owners", agent] }); } });
  const columns: DataTableColumn<PatchRow>[] = [
    { key: "class", label: "Class", minWidth: 220, render: (_, row) => renderLink({ to: jvmHref(agent, "jvm.classes.source", { class: row.class, loader: row.loader }), children: row.class, className: "break-all font-mono underline" }) },
    { key: "loader", label: "Loader" },
    { key: "patched", label: "State", render: (_, row) => row.error ? row.error : row.patched ? "patched" : "original bytes" },
    { key: "leaseExpiresAt", label: "Automatic restore", render: (_, row) => !row.patched ? "—" : !row.leaseExpiresAt ? "No lease; manual restore required" : row.leaseExpiresAt <= Date.now() ? "Restoring…" : `in ${Math.ceil((row.leaseExpiresAt - Date.now()) / 1000)}s` },
    { key: "actions", label: "Actions", render: (_, row) => {
      const owners = (probes.data?.keys ?? []).filter(probe => probe.owners.includes(`${row.loader}|${row.class}`));
      const disabled = !allowed || !row.patched || row.loader.startsWith("~") || change.isPending || !probes.isSuccess;
      return <div className="space-y-2">{owners.length > 0 && <p className="text-xs">{owners.length} active capture(s) record this class. Restoring interrupts their events for this class.</p>}<div className="flex flex-wrap gap-2"><Button variant="outline" disabled={disabled} onClick={() => change.mutate({ row, operation: "restore" })}>Restore</Button><Button variant="outline" disabled={disabled} onClick={() => change.mutate({ row, operation: "lease" })}>Renew 120s</Button></div></div>;
    } },
  ];
  return <Panel title="Bytecode patch register"><div className="space-y-3 min-w-0">
    <p className="text-sm">{patches.data?.filter(row => row.patched).length ?? 0} patched classes. Classes without a lease keep running modified bytes until restored.</p>
    <label className="block text-sm">Filter patches<InputField value={filter} onChange={value => { setFilter(value); persistFilter("patchFilter", value); }} /></label>
    <MutationChoice label="Allow restoring and renewing JVM patches" checked={allowed} onChange={setAllowed} />
    {(patches.error || probes.error || change.error) && <p role="alert">{(patches.error ?? probes.error ?? change.error)?.message}</p>}
    <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={(patches.data ?? []).filter(row => `${row.class} ${row.loader}`.toLowerCase().includes(filter.toLowerCase()))} columns={columns} getRowId={row => `${row.loader}:${row.class}`} loading={patches.isPending} emptyMessage="This JVM is running its original bytes" /></div>
    <Button variant="outline" onClick={() => { void patches.refetch(); void probes.refetch(); }}>Refresh patches</Button>
  </div></Panel>;
}
