import { useState, type ReactNode } from "react";
import { Button, DropdownMenu, Field, InputField, SegmentedControl, type DropdownMenuItem } from "@flanksource/clicky-ui/components";
import { Badge } from "@flanksource/clicky-ui/data";
import { UiAdd, UiChevronDown } from "@flanksource/clicky-ui/icons";
import { cn } from "@flanksource/clicky-ui/utils";
import { DeployAgentDialog } from "../agents/DeployAgentDialog";
import { CAPTURE_BACKENDS, SERVER_KUBECONFIG, backendConnectionTypes, backendForm, type CaptureBackend } from "./backends";
import { connectionsFor, errorAt, type ArmCatalog, type StepProps } from "./catalog";
import { CaptureIcon } from "./CaptureIcon";
import { CaptureList } from "./CaptureList";
import clickyLogo from "./clicky-horizontal.svg";
import clickyLogoDark from "./clicky-horizontal-reversed.svg";
import captainLogo from "./captain-horizontal.svg";
import captainLogoDark from "./captain-horizontal-reversed.svg";
import { GROUPS, SEAMS, addCapture, runTargetNeed, withOnlyAgent, type ArmDraft, type CapturePatch, type Kind, type RunTarget, type Seam } from "./model";
import { ProcessPicker } from "./ProcessPicker";

const GROUP_LOGOS = { clicky: [clickyLogo, clickyLogoDark], captain: [captainLogo, captainLogoDark] };

function ChoiceCard({ selected, onClick, title, children, aside }: { selected: boolean; onClick: () => void; title: string; children?: ReactNode; aside?: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      className={cn("w-full space-y-1 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50", selected ? "border-primary/60 bg-primary/5" : "border-border bg-card")}
    >
      <span className="flex items-start justify-between gap-2">
        <span className="font-medium">{title}</span>
        {aside}
      </span>
      {children && <span className="block text-xs text-muted-foreground">{children}</span>}
    </button>
  );
}

/** Step 1: a saved definition, or captures chosen here. Advances on choice. */
export function SourceStep({ catalog, draft, onPick }: { catalog: ArmCatalog; draft: ArmDraft; onPick: (source: NonNullable<ArmDraft["source"]>) => void }) {
  return (
    <div className="space-y-4">
      <ChoiceCard selected={draft.source?.kind === "captures"} onClick={() => onPick({ kind: "captures" })} title="Choose captures" aside={<span className="rounded-full bg-primary/10 px-2 text-[11px] text-primary">{catalog.kinds.length} kinds</span>}>
        Pick what to record (HTTP, SQL, SQL Server internals, JVM profiles and methods, logs) and where each one runs.
      </ChoiceCard>
      {catalog.definitions.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Or start a saved definition</h3>
          <div className="grid gap-2 @2xl:grid-cols-3">
            {catalog.definitions.map(definition => (
              <ChoiceCard
                key={`${definition.layer}:${definition.name}`}
                selected={draft.source?.kind === "definition" && draft.source.name === definition.name}
                onClick={() => !definition.problem && onPick({ kind: "definition", name: definition.name })}
                title={definition.name}
                aside={<span className="rounded-full border border-border px-2 text-[11px] text-muted-foreground">{definition.layer}</span>}
              >
                <span className="line-clamp-3">{definition.problem ? `Unreadable: ${definition.problem}` : definition.description}</span>
              </ChoiceCard>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/** The targets a seam offers for kind: matching connections or agents with the probe, then Choose later. */
function seamTargets(catalog: ArmCatalog, kind: Kind, seam: Seam, add: (patch: CapturePatch) => void, deploy: () => void): DropdownMenuItem[] {
  const later = { label: <span className="text-muted-foreground">Choose later</span>, onSelect: () => add({ seam }) };
  if (seam === "connection") {
    return [...connectionsFor(kind.connectionTypes, catalog.connections).map(connection => ({ label: connection.name, title: connection.reference, onSelect: () => add({ seam, connection: connection.reference }) })), later];
  }
  if (seam === "agent") {
    const agents = catalog.agents.filter(agent => agent.probes.some(probe => probe.name === kind.name));
    return [
      ...agents.map(agent => ({ label: `${agent.service || agent.agentId} · ${agent.agentId}`, onSelect: () => add({ seam, agent: { mode: "id" as const, agents: [agent.agentId] } }) })),
      { label: <span className="text-primary">Deploy an agent…</span>, onSelect: deploy },
      later,
    ];
  }
  return [];
}

/** Add's menu: one item per seam with its targets as a submenu; a single-seam kind lists its targets directly. */
function addItems(catalog: ArmCatalog, kind: Kind, add: (patch: CapturePatch) => void, deploy: () => void): DropdownMenuItem[] {
  if (kind.seams.length === 1) {
    const seam = kind.seams[0]!;
    const targets = seamTargets(catalog, kind, seam, add, deploy);
    if (targets.length) return targets.map(item => ({ ...item, group: SEAMS[seam].label }));
  }
  return kind.seams.map(seam => {
    const targets = seamTargets(catalog, kind, seam, add, deploy);
    return { label: SEAMS[seam].label, title: SEAMS[seam].description, onSelect: () => add({ seam }), ...(targets.length ? { children: targets } : {}) };
  });
}

function addKind(catalog: ArmCatalog, draft: ArmDraft, kind: Kind, patch: CapturePatch = {}) {
  const offering = catalog.agents.filter(agent => agent.probes.some(probe => probe.name === kind.name)).map(agent => agent.agentId);
  const deployed = catalog.deployed.filter(target => target.state === "deployed").map(target => target.target);
  return addCapture(draft, kind, withOnlyAgent(kind, patch, offering, deployed));
}

function CatalogCard({ title, summary, detail, icon, armed, cost, action }: {
  title: string; summary: string; detail: string; icon: ReactNode; armed: number; cost?: string; action: ReactNode;
}) {
  return (
    <div className={cn("relative grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-lg border p-3", armed ? "border-primary/40 bg-primary/5" : "border-border bg-card")}>
      {armed > 0 && (
        <span aria-label={`${armed} armed`} className="absolute -right-2 -top-2 flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1.5 text-[11px] font-semibold text-primary-foreground shadow-sm">{armed}</span>
      )}
      <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md", armed ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")}>
        {icon}
      </span>
      <p className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium">
        {title}
        {cost && <Badge tone="warning" variant="soft" size="xs">{cost}</Badge>}
      </p>
      {action}
      <div className="col-span-2 col-start-2 space-y-0.5">
        <p className="text-xs text-muted-foreground">{summary}</p>
        <p className="text-[11px] text-muted-foreground">{detail}</p>
      </div>
    </div>
  );
}

function addButton({ title, armed, disabled }: { title: string; armed: number; disabled?: string }) {
  return (
    <Button size="sm" variant="outline" aria-label={`Add ${title}`} disabled={!!disabled} title={disabled}>
      <UiAdd className="mr-1" /> {armed ? "Add another" : "Add"} <UiChevronDown className="ml-1" />
    </Button>
  );
}

function KindCard({ catalog, kind, draft, onChange }: { kind: Kind } & Omit<StepProps, "errors">) {
  const [deploying, setDeploying] = useState(false);
  const armed = draft.captures.filter(capture => capture.kind === kind.name && !capture.backend).length;
  const add = (patch: CapturePatch) => onChange(addKind(catalog, draft, kind, patch));
  return (
    <>
      <CatalogCard title={kind.title} summary={kind.summary} armed={armed} cost={kind.cost}
        detail={kind.seams.map(seam => SEAMS[seam].label).join(" · ")}
        icon={<CaptureIcon kind={kind.name} className="size-5" />}
        action={<DropdownMenu trigger={addButton({ title: kind.title, armed })} align="right"
          menuLabel={`Where ${kind.title} runs`} items={addItems(catalog, kind, add, () => setDeploying(true))} />}
      />
      {deploying && <DeployAgentDialog open onClose={() => setDeploying(false)} onDeployed={agents => add({ seam: "agent", agent: { mode: "id", agents } })} />}
    </>
  );
}

function backendKind(catalog: ArmCatalog, backend: CaptureBackend): Kind | undefined {
  return catalog.kinds.find(kind => kind.name === backend.kind && kind.seams.includes("host") && backendForm(kind, backend));
}

const unavailable = "Unavailable in the server’s start form";

function BackendCard({ catalog, backend, draft, onChange }: { backend: CaptureBackend } & Omit<StepProps, "errors">) {
  const armed = draft.captures.filter(capture => capture.backend === backend.id).length;
  const kind = backendKind(catalog, backend);
  const add = (connection = "") => {
    if (!kind) throw new Error(`${backend.title} is unavailable`);
    onChange(addCapture(draft, kind, { backend: backend.id, seam: "host", connection }));
  };
  const items = (kind ? connectionsFor(backendConnectionTypes(kind, backend), catalog.connections) : []).map(connection => ({
    label: connection.name, title: connection.reference, onSelect: () => add(connection.reference),
  }));
  items.push({ label: backend.provider === "k8s" ? SERVER_KUBECONFIG : "Choose later", title: "", onSelect: () => add() });
  return (
    <CatalogCard title={backend.title} summary={backend.summary} armed={armed}
      detail={kind ? SEAMS.host.label : unavailable}
      icon={<CaptureIcon kind={backend.kind} provider={backend.provider} className="size-5" />}
      action={kind
        ? <DropdownMenu trigger={addButton({ title: backend.title, armed })} align="right" menuLabel={`Where ${backend.title} reads from`} items={items} />
        : addButton({ title: backend.title, armed, disabled: unavailable })}
    />
  );
}

function TraceMenuItem({ kind, provider, title, summary, onClick }: {
  kind: string; provider?: string; title: string; summary: string; onClick?: () => void;
}) {
  return (
    <button type="button" disabled={!onClick} title={onClick ? undefined : unavailable} onClick={onClick}
      className="flex w-full items-start gap-2.5 rounded-md p-2 text-left hover:bg-muted focus-visible:bg-muted disabled:opacity-50">
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
        <CaptureIcon kind={kind} provider={provider} className="size-4" />
      </span>
      <span className="min-w-0">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{summary}</span>
      </span>
    </button>
  );
}

function AddTraceMenu({ catalog, draft, onChange }: Omit<StepProps, "errors">) {
  return (
    <DropdownMenu role="dialog" align={draft.captures.length ? "right" : "left"} menuLabel="Add a trace"
      menuClassName="max-h-[min(36rem,calc(100dvh-5rem))] w-[min(60rem,calc(100vw-2rem))] overflow-auto p-3"
      trigger={<Button size="sm"><UiAdd className="mr-1" /> Add trace <UiChevronDown className="ml-1" /></Button>}>
      {close => {
        const pick = (next: ArmDraft) => { onChange(next); close(); };
        return (
          <div className="grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {GROUPS.map(group => {
              const kinds = catalog.kinds.filter(kind => kind.group === group.id);
              const backends = group.id === "query" ? CAPTURE_BACKENDS : [];
              if (!kinds.length && !backends.length) return null;
              return (
                <section key={group.id} className="space-y-1">
                  <h4 className="px-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{group.label}</h4>
                  {kinds.map(kind => <TraceMenuItem key={kind.name} kind={kind.name} title={kind.title} summary={kind.summary}
                    onClick={() => pick(addKind(catalog, draft, kind))} />)}
                  {backends.map(backend => {
                    const kind = backendKind(catalog, backend);
                    return <TraceMenuItem key={backend.id} kind={backend.kind} provider={backend.provider} title={backend.title} summary={backend.summary}
                      onClick={kind ? () => pick(addCapture(draft, kind, { backend: backend.id, seam: "host" })) : undefined} />;
                  })}
                </section>
              );
            })}
          </div>
        );
      }}
    </DropdownMenu>
  );
}

function EmptyCaptures(props: Omit<StepProps, "errors">) {
  return (
    <div className="relative overflow-hidden rounded-lg border border-dashed border-border">
      <div aria-hidden="true" className="divide-y divide-border/60 opacity-60">
        {["w-1/3", "w-1/2", "w-2/5"].map(width => (
          <div key={width} className="flex items-center gap-3 p-3">
            <span className="size-8 shrink-0 rounded-md bg-muted" />
            <span className="flex-1 space-y-1.5"><span className={cn("block h-3 rounded bg-muted", width)} /><span className="block h-2.5 w-2/3 rounded bg-muted/70" /></span>
            <span className="h-6 w-16 rounded-md bg-muted" />
          </div>
        ))}
      </div>
      <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-background/60 p-2 text-center">
        <p className="text-sm text-muted-foreground">Nothing armed yet. Add a trace to choose what to record.</p>
        <AddTraceMenu {...props} />
      </div>
    </div>
  );
}

/** Step 2: each capture with its own target (seam, connection, agents) and options; a kind may be armed more than once. */
export function CapturesStep(props: StepProps) {
  const { catalog, draft, errors } = props;
  const error = errorAt(errors, "/traces");
  return (
    <div className="space-y-6">
      <section className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">Armed captures <span className="font-normal text-muted-foreground">({draft.captures.length})</span></h3>
          {draft.captures.length > 0 && <AddTraceMenu {...props} />}
        </div>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        {draft.captures.length === 0 ? (
          <EmptyCaptures {...props} />
        ) : (
          <CaptureList {...props} />
        )}
      </section>
      <section className="space-y-3 border-t border-border pt-4">
        <h3 className="text-sm font-semibold">Add a capture</h3>
        {GROUPS.map(group => {
          const kinds = catalog.kinds.filter(kind => kind.group === group.id);
          const backends = group.id === "query" ? CAPTURE_BACKENDS : [];
          if (!kinds.length && !backends.length) return null;
          const logos = group.id === "clicky" || group.id === "captain" ? GROUP_LOGOS[group.id] : undefined;
          return (
            <section key={group.id} className="space-y-2" aria-label={group.label}>
              <header>
                <h4 className="text-sm font-semibold" aria-label={logos ? group.label : undefined}>{logos ? (
                  <>
                    <img src={logos[0]} alt="" className="h-6 w-auto [[data-theme=dark]_&]:hidden" />
                    <img src={logos[1]} alt="" className="hidden h-6 w-auto [[data-theme=dark]_&]:block" />
                  </>
                ) : group.label}</h4>
                <p className="text-xs text-muted-foreground">{group.description}</p>
              </header>
              <div className={cn("grid gap-2 @2xl:grid-cols-2", (group.id === "clicky" || group.id === "sql_server") && "@4xl:grid-cols-3")}>
                {kinds.map(kind => <KindCard key={kind.name} kind={kind} {...props} />)}
                {backends.map(backend => <BackendCard key={backend.id} backend={backend} {...props} />)}
              </div>
            </section>
          );
        })}
      </section>
    </div>
  );
}

const RUN_MODES: { id: RunTarget["mode"]; label: string }[] = [
  { id: "command", label: "Run a command" },
  { id: "pid", label: "Attach to a process" },
  { id: "none", label: "No process" },
];

/** Step 3: the run's own process (a command, a pid, or none) as its captures need. */
export function ProcessStep({ draft, onChange, errors }: StepProps) {
  const need = runTargetNeed(draft);
  // A run with no process of its own cannot stop when it exits.
  const setRun = (run: RunTarget) => onChange({ ...draft, run, window: run.mode === "none" && draft.window.stop === "exit" ? { ...draft.window, stop: "manual" } : draft.window });
  const reason = need === "command" ? "A local proxy capture routes a command's traffic." : need === "pid" ? "A local process capture attaches to a pid." : undefined;
  return (
    <section className="space-y-2">
      {reason && <p className="text-xs text-muted-foreground">{reason}</p>}
      <SegmentedControl<RunTarget["mode"]>
        aria-label="The run's process"
        size="sm"
        value={draft.run.mode}
        onChange={mode => setRun(mode === "command" ? { mode, command: "" } : mode === "pid" ? { mode, pid: null } : { mode })}
        options={RUN_MODES.map(mode => ({ ...mode, disabled: need !== "optional" && mode.id !== need }))}
      />
      {draft.run.mode === "command" && (
        <Field label="Command" htmlFor="arm-command" required={need === "command"} error={errorAt(errors, "/command")} helper="Run with sh -c in the server's working directory">
          <InputField as="textarea" id="arm-command" rows={2} value={draft.run.command} onChange={command => setRun({ mode: "command", command })} placeholder="make test" invalid={!!errorAt(errors, "/command")} />
        </Field>
      )}
      {draft.run.mode === "pid" && <ProcessPicker id="arm-pid" value={draft.run.pid} onChange={pid => setRun({ mode: "pid", pid })} error={errorAt(errors, "/pid")} helper="Running processes on the server's host" />}
      {draft.run.mode === "none" && <p className="text-xs text-muted-foreground">The run records until its duration or until you stop it.</p>}
    </section>
  );
}
