import { useRouter } from "@flanksource/clicky-ui/rpc";
import type { Targets } from "./api";

export function TargetsView({ targets }: { targets: Targets }) {
  const { renderLink } = useRouter();
  const processes = targets.processes ?? [];
  const sessions = targets.sessions ?? [];
  return <div className="space-y-2 text-sm">
    <p>{processes.length} processes · {sessions.length} sessions</p>
    <ul className="max-h-64 space-y-2 overflow-auto">
      {processes.map(process => <li key={process.key} className="min-w-0 space-y-1">
        <div className="flex flex-wrap gap-x-2"><span className="font-medium">{process.name}</span>{renderLink({ to: `/top/${process.pid}`, className: "text-primary underline underline-offset-4", children: `PID ${process.pid}` })}<code className="break-all text-muted-foreground">{process.key}</code></div>
        <details className="text-xs text-muted-foreground"><summary className="cursor-pointer truncate" title={process.command}>{process.command || "Command not recorded"}</summary><p className="mt-1 break-all">{process.command}</p></details>
      </li>)}
      {sessions.map(session => <li key={session.key} className="break-all"><span className="font-medium">{session.agent}</span> · {session.id}{session.error && <p className="text-destructive">{session.error}</p>}</li>)}
    </ul>
  </div>;
}
