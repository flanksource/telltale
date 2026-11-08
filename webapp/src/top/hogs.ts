import type { Usage } from "./api";

export function isPinned(usage: Usage, sampledAt: string): boolean {
  return Object.values(usage.hogs ?? {}).some(until => Date.parse(until) > Date.parse(sampledAt));
}

export function hogLabel(usage: Usage, sampledAt: string): string {
  const resources = ["CPU", "memory", "swap", "compressed"].filter(resource => {
    const until = usage.hogs?.[resource];
    return until !== undefined && Date.parse(until) > Date.parse(sampledAt);
  });
  return resources.length ? `${resources.join("/")} hog` : "";
}

export function mergeHogs(a: Usage["hogs"], b: Usage["hogs"]): Usage["hogs"] {
  if (!a && !b) return undefined;
  const merged = { ...a };
  for (const [resource, until] of Object.entries(b ?? {})) {
    if (!merged[resource] || Date.parse(until) > Date.parse(merged[resource])) merged[resource] = until;
  }
  return merged;
}
