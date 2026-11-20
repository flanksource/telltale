import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { useRunShell, type RunShellTargets } from "./run-shell";

/** Standalone views render inline; an AppShell host owns their slot containers. */
export function RunShellSlot({ name, children }: { name: keyof RunShellTargets; children: ReactNode }) {
  const targets = useRunShell();
  if (!targets) return children;
  const container = targets[name];
  return container ? createPortal(children, container) : null;
}
