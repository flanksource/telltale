import { useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { ClickyNodeView, type ClickyDocument } from "@flanksource/clicky-ui/clicky";
import { failure, traceRecordsUrl, type CaptureResult, type TraceKindInfo } from "./api";

export function TopRowDetail({raw,table,mount,capture,selection,relation}:{raw:Record<string,unknown>;table:ClickyDocument;mount:string;capture:CaptureResult;selection:string;relation:NonNullable<TraceKindInfo["top"]>["relation"]}) {
  const [linked,setLinked]=useState<ClickyDocument|null>(null);
  const [message,setMessage]=useState("");
  const [loading,setLoading]=useState(false);
  const node=(linked ?? table).node.rows?.find(row=>linked || row.cells.seq?.filterValue===raw.seq)?.detail;
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
    {!!id && !linked && <Button variant="outline" size="sm" loading={loading} onClick={()=>void openRelated()}>Open {relation?.label.toLowerCase()} {String(id)}</Button>}
    {linked && <Button variant="ghost" size="sm" onClick={()=>setLinked(null)}>Back to selected row</Button>}
    {message && <p role="status">{message}</p>}
    {node && <ClickyNodeView node={node} />}
  </div>;
}
