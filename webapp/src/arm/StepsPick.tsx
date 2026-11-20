import { useState, type ReactNode } from "react";
import { Button, DropdownMenu, Field, InputField, SegmentedControl, type DropdownMenuItem } from "@flanksource/clicky-ui/components";
import { FlanksourceIcon } from "@flanksource/clicky-ui/connections";
import { Badge } from "@flanksource/clicky-ui/data";
import { UiAdd, UiChevronDown } from "@flanksource/clicky-ui/icons";
import { cn } from "@flanksource/clicky-ui/utils";
import { DeployAgentDialog } from "../agents/DeployAgentDialog";
import { connectionsFor, errorAt, type ArmCatalog, type StepProps } from "./catalog";
import { CaptureList } from "./CaptureList";
import { GROUPS, SEAMS, addCapture, runTargetNeed, withOnlyAgent, type ArmDraft, type CapturePatch, type Kind, type RunTarget, type Seam } from "./model";
import { ProcessPicker } from "./ProcessPicker";

/** Each kind's product logo from @flanksource/icons, by icon name. */
const KIND_ICONS: Record<string, string> = {
  http: "http", http_top: "http", sql: "postgres", sql_top: "sql", sql_deadlock: "sqlserver", sql_plancache: "sqlserver", sql_xevent: "sqlserver",
  jvm_jfr: "java", jvm_trace: "java", jvm_notifications: "java", jvm_ognl: "java", jvm_redefine: "java", jms: "activemq",
  exec: "shell", llm: "ai", logs: "logs", spans: "opentelemetry", entity: "database", task: "console",
};

function KindIcon({ kind, className }: { kind: string; className?: string }) {
  return <FlanksourceIcon name={KIND_ICONS[kind] ?? "database"} className={className} />;
}

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
    return [...connectionsFor(kind, catalog.connections).map(connection => ({ label: connection.name, title: connection.reference, onSelect: () => add({ seam, connection: connection.reference }) })), later];
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

function KindCard({ catalog, kind, draft, onChange }: { kind: Kind } & Omit<StepProps, "errors">) {
  const [deploying, setDeploying] = useState(false);
  const armed = draft.captures.filter(capture => capture.kind === kind.name).length;
  const offering = catalog.agents.filter(agent => agent.probes.some(probe => probe.name === kind.name)).map(agent => agent.agentId);
  const deployed = catalog.deployed.filter(target => target.state === "deployed").map(target => target.target);
  const add = (patch: CapturePatch) => onChange(addCapture(draft, kind, withOnlyAgent(kind, patch, offering, deployed)));
  return (
    <div className={cn("relative grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-lg border p-3", armed ? "border-primary/40 bg-primary/5" : "border-border bg-card")}>
      {armed > 0 && (
        <span aria-label={`${armed} armed`} className="absolute -right-2 -top-2 flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1.5 text-[11px] font-semibold text-primary-foreground shadow-sm">{armed}</span>
      )}
      <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md", armed ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")}>
        <KindIcon kind={kind.name} className="size-5" />
      </span>
      <p className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium">
        {kind.title}
        {kind.cost && <Badge tone="warning" variant="soft" size="xs">{kind.cost}</Badge>}
      </p>
      <DropdownMenu
        trigger={
          <Button size="sm" variant="outline" aria-label={`Add ${kind.title}`}>
            <UiAdd className="mr-1" /> {armed ? "Add another" : "Add"} <UiChevronDown className="ml-1" />
          </Button>
        }
        align="right"
        menuLabel={`Where ${kind.title} runs`}
        items={addItems(catalog, kind, add, () => setDeploying(true))}
      />
      <div className="col-span-2 col-start-2 space-y-0.5">
        <p className="text-xs text-muted-foreground">{kind.summary}</p>
        <p className="text-[11px] text-muted-foreground">runs on: {kind.seams.map(seam => SEAMS[seam].label).join(" · ")}</p>
      </div>
      {deploying && <DeployAgentDialog open onClose={() => setDeploying(false)} onDeployed={agents => add({ seam: "agent", agent: { mode: "id", agents } })} />}
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
        <h3 className="text-sm font-semibold">Armed captures <span className="font-normal text-muted-foreground">({draft.captures.length})</span></h3>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        {draft.captures.length === 0 ? (
          <p className="rounded-lg border border-dashed border-border p-4 text-center text-sm text-muted-foreground">Nothing armed yet. Add a capture below; each one picks where it runs.</p>
        ) : (
          <CaptureList {...props} />
        )}
      </section>
      <section className="space-y-3 border-t border-border pt-4">
        <h3 className="text-sm font-semibold">Add a capture</h3>
        {GROUPS.map(group => {
          const kinds = catalog.kinds.filter(kind => kind.group === group.id);
          if (kinds.length === 0) return null;
          return (
            <section key={group.id} className="space-y-2" aria-label={group.label}>
              <header>
                <h4 className="text-sm font-semibold">{group.label}</h4>
                <p className="text-xs text-muted-foreground">{group.description}</p>
              </header>
              <div className="grid gap-2 @2xl:grid-cols-2">
                {kinds.map(kind => <KindCard key={kind.name} kind={kind} catalog={catalog} draft={draft} onChange={props.onChange} />)}
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
