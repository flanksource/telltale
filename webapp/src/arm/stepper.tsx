import { useCallback, useState, type ReactNode } from "react";
import { cn } from "@flanksource/clicky-ui/utils";
import { UiCheck } from "@flanksource/clicky-ui/icons";

export type StepStatus = "complete" | "current" | "upcoming";

export interface Step<Id extends string = string> {
  id: Id;
  label: string;
  description?: string;
  /** Replaces the description once the step is complete: what was chosen there. */
  summary?: ReactNode;
}

export function stepIndex<Id extends string>(steps: readonly Step<Id>[], id: Id): number {
  const index = steps.findIndex(step => step.id === id);
  if (index < 0) throw new Error(`unknown step ${id} (steps: ${steps.map(step => step.id).join(", ")})`);
  return index;
}

/** Going back is always allowed; going forward needs every step before the target to be ready. */
export function canEnter<Id extends string>(steps: readonly Step<Id>[], current: Id, target: Id, isReady: (id: Id) => boolean): boolean {
  const to = stepIndex(steps, target);
  return to <= stepIndex(steps, current) || steps.slice(0, to).every(step => isReady(step.id));
}

/** The current step of a wizard rail, moving only to steps the ready ones before it allow. */
export function useStepper<Id extends string>({ steps, initial, isReady }: { steps: readonly Step<Id>[]; initial?: Id; isReady: (id: Id) => boolean }) {
  const first = steps[0]?.id;
  if (first === undefined) throw new Error("a stepper needs at least one step");
  const [current, setCurrent] = useState<Id>(initial ?? first);
  const index = stepIndex(steps, current);
  const nextId = steps[index + 1]?.id;
  const canNext = nextId !== undefined && isReady(current);
  const enter = (id: Id) => canEnter(steps, current, id, isReady);
  const goTo = useCallback((id: Id) => setCurrent(id), []);
  return {
    current,
    canNext,
    canEnter: enter,
    goTo: (id: Id) => {
      if (!enter(id)) throw new Error(`step ${id} is not reachable from ${current}`);
      goTo(id);
    },
    next: () => {
      if (!canNext || nextId === undefined) throw new Error(`cannot leave ${current} yet`);
      setCurrent(nextId);
    },
    back: () => {
      const previous = steps[index - 1]?.id;
      if (previous !== undefined) setCurrent(previous);
    },
  };
}

function Marker({ status, index }: { status: StepStatus; index: number }) {
  return (
    <span
      aria-hidden
      className={cn(
        "grid size-5 shrink-0 place-items-center rounded-full text-[10px] font-semibold",
        status === "complete" && "bg-green-600 text-white",
        status === "current" && "bg-primary text-primary-foreground",
        status === "upcoming" && "bg-muted text-muted-foreground",
      )}
    >
      {status === "complete" ? <UiCheck className="text-[11px]" /> : index + 1}
    </span>
  );
}

/** A wizard's rail of steps: vertical cards with each step's description or summary, or compact pills. */
export function Stepper<Id extends string>({ steps, value, onChange, canSelect, variant = "cards", "aria-label": ariaLabel, className }: {
  steps: readonly Step<Id>[];
  value: Id;
  onChange: (id: Id) => void;
  canSelect: (id: Id) => boolean;
  variant?: "cards" | "pills";
  "aria-label": string;
  className?: string;
}) {
  const current = stepIndex(steps, value);
  return (
    <ol aria-label={ariaLabel} className={cn(variant === "cards" ? "flex flex-col gap-1" : "flex flex-wrap items-center gap-1", className)}>
      {steps.map((step, index) => {
        const status: StepStatus = index === current ? "current" : index < current ? "complete" : "upcoming";
        const selectable = index === current || canSelect(step.id);
        const detail = status === "complete" && step.summary !== undefined ? step.summary : step.description;
        return (
          <li key={step.id} className={variant === "cards" ? "w-full" : undefined}>
            <button
              type="button"
              disabled={!selectable}
              aria-current={index === current ? "step" : undefined}
              onClick={() => onChange(step.id)}
              className={cn(
                "flex items-center gap-2",
                variant === "pills" ? "rounded-full border px-2.5 py-1 text-xs" : "w-full rounded-md px-2 py-1.5",
                index === current ? (variant === "pills" ? "border-primary bg-primary/10 text-primary" : "bg-primary/10") : variant === "pills" && "border-border",
                selectable && index !== current && "hover:bg-muted",
                !selectable && "opacity-60",
              )}
            >
              <Marker status={status} index={index} />
              {variant === "pills" ? (
                <span className="whitespace-nowrap">{`${index + 1}. ${step.label}`}</span>
              ) : (
                <span className="min-w-0 text-left">
                  <span className="block truncate text-xs font-medium">{step.label}</span>
                  {detail !== undefined && <span className="block truncate text-[11px] text-muted-foreground">{detail}</span>}
                </span>
              )}
            </button>
          </li>
        );
      })}
    </ol>
  );
}
