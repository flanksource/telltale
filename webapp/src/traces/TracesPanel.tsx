import { useMemo } from "react";
import { TraceFeed } from "@flanksource/clicky-ui/data";
import { traceFeedSource } from "./feed";
import { type RunTraces } from "./api";

/** The captures one run armed, read from its store. */
export function TracesPanel({ run }: { run: RunTraces }) {
	const source = useMemo(() => traceFeedSource(run.mount), [run.mount]);
  const kinds = [...new Set([...(run.captures ?? []).map(capture => capture.kind), ...(run.process ? ["process", "stacks"] : [])])];
  const live = run.status === "running" && kinds.length > 0;
  return <TraceFeed kinds={kinds} loadPage={source.loadPage} renderCapture={capture => capture.kind === "process" ? null : undefined} {...(live && source.follow ? { follow: source.follow } : {})} />;
}
