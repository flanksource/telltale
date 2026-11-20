import { useRouter } from "@flanksource/clicky-ui/rpc";
import { TraceRecords } from "../traces/TraceRecords";

export default function RecordsPage({ run, kind, stream }: { run: string; kind: string; stream: string }) {
  const { renderLink } = useRouter();
  return <div className="space-y-4 min-w-0">
    <h1 className="text-2xl font-semibold">Query {kind} records</h1>
    <p className="text-sm text-muted-foreground break-all">{run} · {stream}</p>
    {renderLink({ to: `/?run=${encodeURIComponent(run)}`, children: "Back to trace run", className: "text-sm underline" })}
    <div className="h-[70vh] min-h-[32rem]"><TraceRecords run={run} kind={kind} stream={stream} /></div>
  </div>;
}
