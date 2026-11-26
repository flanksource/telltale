import { useEffect, useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { UiArrowLeft, UiArrowRight } from "@flanksource/clicky-ui/icons";
import type { RunTraces } from "../traces/api";
import { captureBackend } from "./backends";
import type { ArmCatalog } from "./catalog";
import { captureTargetText, stepForError, validate, type ArmDraft, type DraftError, type StepId } from "./model";
import { Stepper, useStepper, type Step } from "./stepper";
import { ReviewStep, WindowStep } from "./StepsFinish";
import { CapturesStep, ProcessStep, SourceStep } from "./StepsPick";

const COPY: Record<StepId, { label: string; title: string; description: string }> = {
  source: { label: "Start from", title: "What should this run record?", description: "Build the captures here, or start a definition someone saved." },
  captures: { label: "Captures", title: "Arm the captures", description: "Each capture says where it runs (a connection, agents, a proxy) and carries its own options. Arm a kind twice for two targets." },
  process: { label: "Process", title: "The run's process", description: "The command to run or the process to attach to, when a capture needs one." },
  window: { label: "Window", title: "How long to record", description: "When the run stops and how often it reads checkpoints." },
  review: { label: "Review", title: "Review and start", description: "The exact request the server receives, and the CLI line for the same run." },
};

function stepSummary(id: StepId, draft: ArmDraft): string | undefined {
  switch (id) {
    case "source":
      return draft.source?.kind === "definition" ? `definition ${draft.source.name}` : draft.source ? "captures" : undefined;
    case "captures":
      return draft.captures.map(capture => `${captureBackend(capture)?.title ?? capture.kind} → ${captureTargetText(capture) || "?"}`).join(", ");
    case "process":
      return draft.run.mode === "command" ? draft.run.command || "no command" : draft.run.mode === "pid" ? `pid ${draft.run.pid ?? "?"}` : "no process";
    case "window":
      return draft.window.stop === "duration" ? `for ${draft.window.duration}` : draft.window.stop === "exit" ? "until the process exits" : "until stopped";
    case "review":
      return undefined;
  }
}

/** The arm wizard: a rail of steps beside one question at a time, ending in the start request. */
export function ArmWizard({ catalog, initial, initialStep, onStarted }: { catalog: ArmCatalog; initial: ArmDraft; initialStep?: StepId; onStarted: (run: RunTraces) => void }) {
  const [draft, setDraftState] = useState<ArmDraft>(initial);
  /** Steps whose errors are shown: those the user tried to leave, or was sent back to. */
  const [shown, setShown] = useState<Set<StepId>>(new Set());
  /** The server's refusals of the last start, until the draft changes. */
  const [refused, setRefused] = useState<DraftError[]>([]);
  const setDraft = (next: ArmDraft) => {
    setDraftState(next);
    setRefused([]);
  };
  const errors = [...validate(draft, catalog.kinds), ...refused];
  const errorsFor = (id: StepId) => errors.filter(error => error.instancePath !== "" && stepForError(error) === id);

  const ids: StepId[] = draft.source?.kind === "definition" ? ["source", "process", "window", "review"] : ["source", "captures", "process", "window", "review"];
  const steps: Step<StepId>[] = ids.map(id => {
    const summary = stepSummary(id, draft);
    return { id, label: COPY[id].label, description: COPY[id].description, ...(summary ? { summary } : {}) };
  });
  const isReady = (id: StepId) => (id === "source" ? draft.source !== null : errorsFor(id).length === 0);
  const stepper = useStepper({ steps, ...(initialStep ? { initial: initialStep } : {}), isReady });
  const current = stepper.current;
  // The first step advances on choice; the jump waits for the draft that makes it reachable.
  const [advanceTo, setAdvanceTo] = useState<StepId | null>(null);
  useEffect(() => {
    if (advanceTo === null) return;
    setAdvanceTo(null);
    stepper.goTo(advanceTo);
  }, [advanceTo, stepper]);
  const stepErrors = shown.has(current) ? errorsFor(current) : [];

  const next = () => {
    if (!stepper.canNext) {
      setShown(previous => new Set(previous).add(current));
      return;
    }
    stepper.next();
  };
  const props = { catalog, draft, onChange: setDraft, errors: stepErrors };

  return (
    <div className="@container">
      <div className="grid gap-4 @3xl:grid-cols-[230px_minmax(0,1fr)]">
        <nav aria-label="Arm steps" className="@3xl:border-r @3xl:border-border @3xl:pr-3">
          <Stepper aria-label="Arm steps" steps={steps} value={current} onChange={stepper.goTo} canSelect={stepper.canEnter} className="hidden @3xl:flex" />
          <Stepper aria-label="Arm steps (compact)" steps={steps} value={current} onChange={stepper.goTo} canSelect={stepper.canEnter} variant="pills" className="@3xl:hidden" />
        </nav>
        <section className="min-w-0 space-y-4">
          <header className="space-y-1">
            <h2 className="text-lg font-semibold">{COPY[current].title}</h2>
            <p className="text-sm text-muted-foreground">{COPY[current].description}</p>
          </header>
          {current === "source" && (
            <SourceStep
              catalog={catalog}
              draft={draft}
              onPick={source => {
                setDraft({ ...draft, source });
                setAdvanceTo(source.kind === "definition" ? "process" : "captures");
              }}
            />
          )}
          {current === "captures" && <CapturesStep {...props} />}
          {current === "process" && <ProcessStep {...props} />}
          {current === "window" && <WindowStep {...props} />}
          {current === "review" && (
            <ReviewStep
              catalog={catalog}
              draft={draft}
              onStarted={onStarted}
              onFix={(step, errors) => {
                setRefused(errors.filter(error => !validate(draft, catalog.kinds).some(local => local.instancePath === error.instancePath)));
                setShown(previous => new Set([...previous, ...errors.flatMap(error => stepForError(error) ?? [])]));
                stepper.goTo(step);
              }}
            />
          )}
          {current !== "source" && current !== "review" && (
            <footer className="flex items-center justify-between border-t border-border pt-3">
              <Button variant="outline" size="sm" onClick={stepper.back}><UiArrowLeft className="mr-1" /> Back</Button>
              <div className="flex items-center gap-3">
                {stepErrors.length > 0 && <span role="status" className="text-xs text-destructive">{stepErrors.length === 1 ? "1 thing to fix" : `${stepErrors.length} things to fix`}</span>}
                <Button size="sm" onClick={next}>Next <UiArrowRight className="ml-1" /></Button>
              </div>
            </footer>
          )}
          {current === "review" && <Button variant="outline" size="sm" onClick={stepper.back}><UiArrowLeft className="mr-1" /> Back</Button>}
        </section>
      </div>
    </div>
  );
}
