import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Checkbox, CopyButton, Field, InputField, SegmentedControl } from "@flanksource/clicky-ui/components";
import { Badge, Callout } from "@flanksource/clicky-ui/data";
import { UiLoader, UiPlay } from "@flanksource/clicky-ui/icons";
import { StartRunError, startRun, type RunTraces } from "../traces/api";
import { captureBackend } from "./backends";
import { errorAt, type ArmCatalog, type StepProps } from "./catalog";
import { SEAMS, buildStartRequest, captureAt, capturePointers, captureTargetText, cliEquivalent, kindByName, stepForError, validate, type ArmDraft, type DraftError, type StepId, type StopMode } from "./model";

const STOPS: { id: StopMode; label: string }[] = [
  { id: "exit", label: "When the process exits" },
  { id: "duration", label: "After a duration" },
  { id: "manual", label: "When I stop it" },
];

/** Step 4: how long the run records and how often it checkpoints. */
export function WindowStep({ draft, onChange, errors }: StepProps) {
  const setWindow = (patch: Partial<ArmDraft["window"]>) => onChange({ ...draft, window: { ...draft.window, ...patch } });
  return (
    <div className="space-y-4">
      <Field label="Stop">
        <SegmentedControl<StopMode>
          aria-label="When the run stops"
          size="sm"
          value={draft.window.stop}
          onChange={stop => setWindow({ stop })}
          options={STOPS.map(stop => (stop.id === "exit" && draft.run.mode === "none" ? { ...stop, disabled: true, title: "The run has no process of its own" } : stop))}
        />
      </Field>
      <div className="grid gap-3 @xl:grid-cols-2">
        {draft.window.stop === "duration" && (
          <Field label="Duration" htmlFor="arm-duration" required error={errorAt(errors, "/duration")} helper="e.g. 30s, 5m, 1h">
            <InputField id="arm-duration" value={draft.window.duration} onChange={duration => setWindow({ duration })} invalid={!!errorAt(errors, "/duration")} />
          </Field>
        )}
        <Field label="Checkpoint interval" htmlFor="arm-every" error={errorAt(errors, "/every")} helper="Read checkpointed captures (JFR, plan cache, heap) this often; empty reads only at stop">
          <InputField id="arm-every" value={draft.window.every} onChange={every => setWindow({ every })} placeholder="10s" invalid={!!errorAt(errors, "/every")} />
        </Field>
      </div>
      {draft.source?.kind === "captures" && (
        <Checkbox checked={draft.window.facets} onChange={facets => setWindow({ facets })} label="Read each process's open files, sockets and environment into the process tree" />
      )}
    </div>
  );
}

function ReviewRows({ catalog, draft }: { catalog: ArmCatalog; draft: ArmDraft }) {
  if (draft.source?.kind === "definition") return <p className="text-sm">Starts the saved definition <span className="font-mono">{draft.source.name}</span>.</p>;
  const pointers = capturePointers(draft, catalog.kinds);
  return (
    <ul className="divide-y divide-border rounded-lg border border-border text-sm">
      {draft.captures.map(capture => (
        <li key={capture.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-1.5">
          <span className="flex items-center gap-2">
            <span className="font-medium">{captureBackend(capture)?.title ?? kindByName(catalog.kinds, capture.kind).title}</span>
            <span className="font-mono text-[11px] text-muted-foreground">{pointers.get(capture.id)}</span>
          </span>
          <span className="flex items-center gap-2 text-xs text-muted-foreground"><Badge size="xs" variant="outline">{SEAMS[capture.seam].label}</Badge><span className="font-mono">{captureTargetText(capture) || "—"}</span></span>
        </li>
      ))}
    </ul>
  );
}

const STEP_LABEL: Record<StepId, string> = { source: "Start from", captures: "Captures", process: "Process", window: "Window", review: "Review" };

/** Step 5: the request, the CLI line, and Start; a refusal names the step that owns each field. */
export function ReviewStep({ catalog, draft, onFix, onStarted }: { catalog: ArmCatalog; draft: ArmDraft; onFix: (step: StepId, errors: DraftError[]) => void; onStarted: (run: RunTraces) => void }) {
  const queryClient = useQueryClient();
  const request = buildStartRequest(draft, catalog.kinds);
  const cli = cliEquivalent(draft, catalog.kinds);
  const start = useMutation({
    mutationFn: async () => {
      const local = validate(draft, catalog.kinds);
      if (local.length) throw new StartRunError(local);
      return startRun(request);
    },
    onSuccess: run => {
      void queryClient.invalidateQueries({ queryKey: ["trace-runs"] });
      onStarted(run);
    },
  });
  const refused: DraftError[] = start.error instanceof StartRunError
    ? start.error.errors.map(error => {
      const captureId = captureAt(draft, catalog.kinds, error.instancePath);
      return { ...error, ...(captureId ? { captureId } : {}) };
    })
    : [];
  const json = JSON.stringify(request, null, 2);
  return (
    <div className="space-y-4">
      <ReviewRows catalog={catalog} draft={draft} />
      <div className="space-y-1">
        <div className="flex items-center justify-between"><h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">POST /api/traces</h3><CopyButton value={json} label="Copy request" /></div>
        <pre className="max-h-72 overflow-auto rounded-md bg-muted p-2 font-mono text-xs">{json}</pre>
      </div>
      <div className="space-y-1">
        <div className="flex items-center justify-between"><h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">CLI</h3>{"command" in cli && <CopyButton value={cli.command} label="Copy command" />}</div>
        {"command" in cli ? <pre className="overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-2 font-mono text-xs">{cli.command}</pre> : <p className="text-xs text-muted-foreground">{cli.gap}</p>}
      </div>
      {refused.length > 0 && (
        <Callout variant="caution" label="Not started">
          <ul className="space-y-1">
            {refused.map(error => {
              // A refusal of the request as a whole, such as an agent refusing to arm, has no field to fix.
              const step = error.instancePath === "" ? undefined : stepForError(error);
              return (
                <li key={error.instancePath + error.message} className="flex flex-wrap items-center gap-2">
                  {error.instancePath && <code className="text-xs">{error.instancePath}</code>} {error.message}
                  {step && <Button size="sm" variant="outline" onClick={() => onFix(step, refused)}>Fix in {STEP_LABEL[step]}</Button>}
                </li>
              );
            })}
          </ul>
        </Callout>
      )}
      {start.error && !(start.error instanceof StartRunError) && <p role="alert" className="text-sm text-destructive">{start.error.message}</p>}
      <Button onClick={() => start.mutate()} disabled={start.isPending}>
        {start.isPending ? <UiLoader className="mr-1 animate-spin" /> : <UiPlay className="mr-1" />} Start run
      </Button>
    </div>
  );
}
