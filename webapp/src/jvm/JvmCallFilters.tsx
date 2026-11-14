import { Button } from "@flanksource/clicky-ui/components";
import type { ProbeOptions, ValueMatch } from "./captures";
import { fieldClass } from "./diagnostics";

type Filters = Pick<ProbeOptions, "minCost" | "exceptionsOnly" | "match">;

const blankMatch: ValueMatch = { on: "arg", index: 0, path: "", op: "equals", value: "" };

/** Which root calls the agent keeps; everything else is counted as filtered, not recorded. */
export function JvmCallFilters({ filters, onChange }: { filters: Filters; onChange: (next: Filters) => void }) {
  const setMatch = (position: number, patch: Partial<ValueMatch>) => onChange({ ...filters, match: filters.match.map((row, index) => index === position ? { ...row, ...patch } : row) });
  return <fieldset className="space-y-3"><legend className="mb-2 text-sm font-medium">Keep only calls</legend>
    <p className="text-xs text-muted-foreground">The agent holds each outermost call and keeps it, with everything nested in it, only when it passes every filter. The rest are counted as filtered.</p>
    <div className="flex flex-wrap items-end gap-4">
      <label className="block text-sm">Minimum cost<input className={fieldClass} placeholder="250ms" value={filters.minCost} onChange={event => onChange({ ...filters, minCost: event.target.value })} /></label>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={filters.exceptionsOnly} onChange={event => onChange({ ...filters, exceptionsOnly: event.target.checked })} />Only calls that threw</label>
    </div>
    {filters.match.map((row, position) => <div key={position} className="flex flex-wrap items-end gap-2" aria-label={`Match ${position + 1}`} role="group">
      <label className="block text-sm">Value<select className={fieldClass} value={row.on} onChange={event => setMatch(position, { on: event.target.value as ValueMatch["on"] })}><option value="arg">Argument</option><option value="return">Return value</option></select></label>
      {row.on === "arg" && <label className="block w-24 text-sm">Index<input type="number" min={0} className={fieldClass} value={row.index} onChange={event => setMatch(position, { index: Number(event.target.value) })} /></label>}
      <label className="block text-sm">Path<input className={fieldClass} placeholder="customer.name" value={row.path} onChange={event => setMatch(position, { path: event.target.value })} /></label>
      <label className="block text-sm">Operator<select className={fieldClass} value={row.op} onChange={event => setMatch(position, { op: event.target.value as ValueMatch["op"] })}><option value="equals">equals</option><option value="contains">contains</option><option value="regex">regex</option></select></label>
      <label className="block text-sm">Text<input className={fieldClass} value={row.value} onChange={event => setMatch(position, { value: event.target.value })} /></label>
      <Button type="button" variant="outline" aria-label={`Remove match ${position + 1}`} onClick={() => onChange({ ...filters, match: filters.match.filter((_, index) => index !== position) })}>Remove</Button>
    </div>)}
    <Button type="button" variant="outline" onClick={() => onChange({ ...filters, match: [...filters.match, blankMatch] })}>Add value match</Button>
  </fieldset>;
}
