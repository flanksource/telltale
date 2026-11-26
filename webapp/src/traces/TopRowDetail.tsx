import { useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { ClickyNodeView, type ClickyDocument } from "@flanksource/clicky-ui/clicky";
import { CodeBlock } from "@flanksource/clicky-ui/data";
import { failure, traceRecordsUrl, type CaptureResult, type TraceKindInfo } from "./api";

export function TopRowDetail({raw,table,mount,capture,selection,relation}:{raw:Record<string,unknown>;table:ClickyDocument;mount:string;capture:CaptureResult;selection:string;relation:NonNullable<TraceKindInfo["top"]>["relation"]}) {
  const [linked,setLinked]=useState<ClickyDocument|null>(null);
  const [message,setMessage]=useState("");
  const [loading,setLoading]=useState(false);
  const node=(linked ?? table).node.rows?.find(row=>linked || row.cells.seq?.filterValue===raw.seq)?.detail;
  const [first, ...remaining] = node?.children ?? [];
  const candidate = node?.kind === "code" ? node : first;
  const sql = raw.engine === "sqlserver" && candidate?.kind === "code" && candidate.language === "sql" ? candidate.source : undefined;
  const detail = sql ? (node && remaining.length ? { ...node, children: remaining } : null)
    : node ?? linked?.node ?? { kind: "code" as const, language: "json", source: JSON.stringify(raw, null, 2) };
  const id=relation ? raw[relation.column] : undefined;
  async function openRelated() {
    if (!relation || !id) return;
    setLoading(true);setMessage("");
    try {
      const response=await fetch(traceRecordsUrl(mount,capture.ref,{view:"top",frame:selection,filter:`${relation.target}=${String(id)}`,limit:"1"}));
      if (!response.ok) throw await failure(response,"Read related row");
      const result=await response.json() as {count:number;table:ClickyDocument};
      if (result.count===0) setMessage(`${relation.label} ${String(id)} was omitted from this completed sample.`);
      else setLinked(result.table);
    } catch(error) {setMessage(error instanceof Error?error.message:String(error));}
    finally {setLoading(false);}
  }
  return <div className="flex min-h-0 flex-col gap-3">
    {sql && <CodeBlock language="sql" source={sql} copyable />}
    {!!id && !linked && <Button className={sql ? "self-start" : undefined} variant="outline" size="sm" loading={loading} onClick={()=>void openRelated()}>Open {relation?.label.toLowerCase()} {String(id)}</Button>}
    {linked && <Button className={sql ? "self-start" : undefined} variant="ghost" size="sm" onClick={()=>setLinked(null)}>Back to selected row</Button>}
    {message && <p role="status">{message}</p>}
    {detail && <ClickyNodeView node={detail} />}
  </div>;
}
