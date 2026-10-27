import { useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { useQueryClient } from "@tanstack/react-query";
import { TOP_ENDPOINT } from "./api";
import { failure } from "../traces/api";

interface Target { key: string; pid: number; name: string; command: string }

export function ProcessActions({ process }: { process: Target }) {
  const client = useQueryClient();
  const [action, setAction] = useState<"terminate" | "renice">();
  const [nice, setNice] = useState("10");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const valid = /^-?\d+$/.test(nice) && Number(nice) >= -20 && Number(nice) <= 19;
  async function apply() {
    setPending(true);
    setError("");
    try {
      const response = await fetch(`${TOP_ENDPOINT}/action`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: process.key, action, ...(action === "renice" ? { nice: Number(nice) } : {}) }),
      });
      if (!response.ok) throw await failure(response, `${action} pid ${process.pid}`);
      setMessage(action === "terminate" ? `SIGTERM sent to PID ${process.pid}.` : `PID ${process.pid} nice set to ${nice}.`);
      setAction(undefined);
      await client.invalidateQueries({ queryKey: ["top-processes"] });
      await client.invalidateQueries({ queryKey: ["top-live"] });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : String(caught));
    } finally {
      setPending(false);
    }
  }
  return (
    <div className="space-y-2" onClick={event => event.stopPropagation()}>
      <div className="flex gap-2">
        <Button disabled={pending || process.pid <= 1} onClick={() => { setAction("terminate"); setMessage(""); setError(""); }}>Terminate</Button>
        <Button disabled={pending || process.pid <= 1} onClick={() => { setAction("renice"); setMessage(""); setError(""); }}>Renice</Button>
      </div>
      {action && (
        <form className="space-y-2 rounded border border-border bg-card p-3" aria-label={`${action} PID ${process.pid}`} onSubmit={event => { event.preventDefault(); void apply(); }}>
          <p>{action === "terminate" ? "Send SIGTERM to" : "Change CPU scheduling priority for"} {process.name} (PID {process.pid})?</p>
          <p className="max-w-80 break-all text-xs text-muted-foreground">{process.command}</p>
          {action === "renice" && <label className="block">Nice value (-20 to 19)
            <input type="number" min={-20} max={19} step={1} required value={nice} onChange={event => setNice(event.target.value)} className="ml-2 w-20 rounded border border-border bg-background px-2 py-1" />
            <span className="block text-xs text-muted-foreground">Higher values reduce priority. Raising priority may require elevated permissions.</span>
          </label>}
          <div className="flex gap-2">
            <Button type="submit" disabled={pending || (action === "renice" && !valid)}>{pending ? "Applying…" : "Confirm"}</Button>
            <Button type="button" disabled={pending} onClick={() => { setAction(undefined); setError(""); }}>Cancel</Button>
          </div>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
    </div>
  );
}
