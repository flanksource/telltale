import { useEffect, useState } from "react";
import { MonacoEditor, MonacoProvider, type Monaco, type MonacoEditorProps } from "@flanksource/clicky-ui/monaco";
import EditorWorker from "monaco-editor/esm/vs/editor/editor.worker.js?worker";
import type { SourceEditorProps } from "./SourceEditor";

const getWorker = () => new EditorWorker();

export default function SourceEditorInput({ diagnostics = [], ...props }: SourceEditorProps) {
  const [mounted, setMounted] = useState<{ instance: Parameters<NonNullable<MonacoEditorProps["onMount"]>>[0]; monaco: Monaco }>();
  useEffect(() => {
    const model = mounted?.instance.getModel();
    if (!model || !mounted) return;
    mounted.monaco.editor.setModelMarkers(model, "jvm-compiler", diagnostics.filter(item => item.line > 0).map(item => ({
      severity: item.kind === "ERROR" ? mounted.monaco.MarkerSeverity.Error : mounted.monaco.MarkerSeverity.Warning,
      message: item.message, startLineNumber: item.line, endLineNumber: item.line,
      startColumn: Math.max(1, item.column), endColumn: Math.max(1, item.column) + 1,
    })));
    return () => { if (!model.isDisposed()) mounted.monaco.editor.setModelMarkers(model, "jvm-compiler", []); };
  }, [diagnostics, mounted]);
  return <MonacoProvider getWorker={getWorker}><MonacoEditor {...props} height="20rem" onMount={(instance, monaco) => { instance.updateOptions({ ariaLabel: "Source" }); setMounted({ instance, monaco }); }} /></MonacoProvider>;
}
