import type { FilterBarDurationUnit, FilterBarDurationValue } from "@flanksource/clicky-ui/components";
import type { SearchValues } from "./api";

// The pure side of the trace search bar: how its time range and duration
// controls map onto the server's request (lookback, from/until and
// minDuration/maxDuration), and how operations read.

/** The server's minSpans that "Hide single-span traces" sets. */
export const MULTI_SPAN = 2;

const RELATIVE = /^now(?:-(\d+)([smhdwMy]))?$/;
const UNIT_HOURS: Record<string, number> = { d: 24, w: 168, M: 720, y: 8760 };
const UNIT_MS: Record<string, number> = { s: 1e3, m: 6e4, h: 36e5, d: 864e5, w: 6048e5, M: 2592e6, y: 31536e6 };

/** A relative offset as a Go duration: s, m and h as they are, longer units in hours. */
function goDuration(count: number, unit: string): string {
  return unit in UNIT_HOURS ? `${count * UNIT_HOURS[unit]!}h` : `${count}${unit}`;
}

/** The time range control's value for a request: its lookback as now-<lookback>, or its from/until as they are. */
export function rangeOf(value: SearchValues): { from: string; to: string } {
  if (value.from || value.until) return { from: value.from ?? "", to: value.until ?? "now" };
  return { from: value.lookback ? `now-${value.lookback}` : "", to: "now" };
}

/** An instant of the range control as RFC3339: now-relative against now, or a date-time the browser reads. */
function instant(text: string, now: number): string {
  const relative = RELATIVE.exec(text);
  if (relative) return new Date(now - (relative[1] ? Number(relative[1]) * UNIT_MS[relative[2]!]! : 0)).toISOString();
  const parsed = Date.parse(text);
  if (Number.isNaN(parsed)) throw new Error(`Could not read the time ${JSON.stringify(text)}: use now, an offset such as now-1h, or a date-time`);
  return new Date(parsed).toISOString();
}

/** The request's window for a range: a lookback when it ends now and starts now-relative, otherwise from/until as instants. */
export function windowOf(from: string, to: string, now: number): Pick<SearchValues, "lookback" | "from" | "until"> {
  const start = from.trim();
  const end = to.trim() || "now";
  const relative = RELATIVE.exec(start);
  if (end === "now" && (!start || (relative && relative[1]))) return { lookback: relative ? goDuration(Number(relative[1]), relative[2]!) : undefined, from: undefined, until: undefined };
  return { lookback: undefined, from: start ? instant(start, now) : undefined, until: end === "now" ? undefined : instant(end, now) };
}

const DURATION = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/;

function bound(text: string | undefined, edge: "min" | "max"): FilterBarDurationValue {
  if (!text) return {};
  const match = DURATION.exec(text);
  if (!match) throw new Error(`${edge}Duration must be a number of ms, s, m or h, not ${JSON.stringify(text)}`);
  return { [edge]: match[1], [`${edge}Unit`]: match[2] as FilterBarDurationUnit };
}

/** The duration control's value for a request's minDuration and maxDuration. */
export function durationOf(value: SearchValues): FilterBarDurationValue {
  return { ...bound(value.minDuration, "min"), ...bound(value.maxDuration, "max") };
}

/** The request's minDuration and maxDuration for the duration control, a bare bound in seconds. */
export function durationRequest(value: FilterBarDurationValue): Pick<SearchValues, "minDuration" | "maxDuration"> {
  const edge = (amount: string | undefined, unit: FilterBarDurationUnit | undefined) => (amount?.trim() ? `${amount.trim()}${unit ?? "s"}` : undefined);
  return { minDuration: edge(value.min, value.minUnit), maxDuration: edge(value.max, value.maxUnit) };
}

/** `invoke_agent /abs/path/.captain/prompts/jira/x.prompt` reads `invoke_agent jira/x.prompt`. */
export const shortOperation = (operation: string) => operation.replace(/ \/\S*\/prompts\//, " ");

/** A W3C (32 hex) or Jaeger 64-bit (16 hex) trace ID. */
export const isTraceId = (text: string) => /^[0-9a-f]{16}([0-9a-f]{16})?$/i.test(text.trim());
