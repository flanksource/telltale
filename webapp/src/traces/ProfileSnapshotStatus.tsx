import type { SampleFrame } from "@flanksource/clicky-ui/data";

export interface ProfileFrame extends SampleFrame {
  tick?: number;
  summary?: SampleFrame["summary"] & {
    complete?: boolean;
    dropped?: number;
    profileId?: string;
    receivedAt?: string;
    pollIntervalMs?: number;
  };
}

export function ProfileSnapshotStatus({ frames, live, now = Date.now() }: { frames: ProfileFrame[]; live: boolean; now?: number }) {
  const latest = new Map<string, ProfileFrame>();
  for (const frame of frames) {
    const previous = latest.get(frame.source);
    if (!previous || (frame.tick ?? 0) >= (previous.tick ?? 0)) latest.set(frame.source, frame);
  }
  const notices = (live ? [...latest.values()] : frames).flatMap(frame => {
    const summary = frame.summary;
    if (!summary?.profileId) return [];
    const messages: string[] = [];
    if (summary.complete === false) messages.push("Incomplete snapshot; some active operations may be missing.");
    if (summary.dropped) messages.push(`${summary.dropped} observations dropped.`);
    if (frame.limit > 0 && frame.observed > frame.limit) messages.push(`Top limit: showing ${frame.retained} of ${frame.observed} active operations.`);
    if (live && summary.receivedAt && summary.pollIntervalMs && now - Date.parse(summary.receivedAt) > summary.pollIntervalMs * 2) {
      messages.push("Snapshot is stale; waiting for a new observation.");
    }
    return messages.length ? [<p key={frame.id} role="status" className="shrink-0 text-sm text-muted-foreground">{frame.source}: {messages.join(" ")}</p>] : [];
  });
  return <>{notices}</>;
}
