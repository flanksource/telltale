import { useContext, useEffect } from "react";
import { Checkbox } from "@flanksource/clicky-ui/components";
import { JvmRuntime } from "./runtime";

export function MutationChoice({ checked, onChange, label }: { checked: boolean; onChange: (value: boolean) => void; label: string }) {
  const runtime = useContext(JvmRuntime);
  useEffect(() => { if (runtime !== true && checked) onChange(false); }, [runtime, checked, onChange]);
  return <div className="space-y-1"><label className="flex items-start gap-2 text-sm"><Checkbox checked={checked} disabled={runtime !== true} onChange={onChange} />{label}</label>{runtime !== true && <p className="text-xs text-muted-foreground">{runtime === false ? "Runtime mutations are disabled for this agent." : "Checking runtime capabilities…"}</p>}</div>;
}
