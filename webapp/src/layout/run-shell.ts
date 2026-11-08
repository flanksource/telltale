import { createContext, useContext } from "react";

export interface RunShellTargets {
  nav: HTMLElement | null;
  header: HTMLElement | null;
  actions: HTMLElement | null;
}

export const RunShellContext = createContext<RunShellTargets | null>(null);
export const useRunShell = () => useContext(RunShellContext);
