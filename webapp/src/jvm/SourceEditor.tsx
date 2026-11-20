import { lazy, Suspense } from "react";
import type { Compilation } from "./diagnostics";

const Editor = lazy(() => import("./SourceEditorInput"));

export interface SourceEditorProps { value: string; onChange: (value: string) => void; language: string; path: string; diagnostics?: Compilation["diagnostics"] }

export function SourceEditor(props: SourceEditorProps) {
  return <div className="min-w-0"><Suspense fallback={<p role="status" className="p-3 text-sm">Loading source editor…</p>}><Editor {...props} /></Suspense></div>;
}
