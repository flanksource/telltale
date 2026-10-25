import { Gauge, StatStrip, type GaugeTone } from "@flanksource/clicky-ui/data";
import type { HostUsage, Overhead } from "./api";
import { formatBytes, formatRate } from "./groups";

const WARN_PERCENT = 60;
const DANGER_PERCENT = 85;

export function toneOf(percent: number): GaugeTone {
  if (percent >= DANGER_PERCENT) return "danger";
  if (percent >= WARN_PERCENT) return "warning";
  return "success";
}

const tenth = (value: number) => Math.round(value * 10) / 10;

const percentOf = (used: number, total: number) => (total > 0 ? (100 * used) / total : 0);

/** A CPU share as text; the first tick has none yet, which reads as a dash and not as 0%. */
const shareText = (percent: number | undefined) => (percent === undefined ? "–" : `${tenth(percent)}%`);

const iopsText = (ops: number | undefined) => (ops === undefined ? "–" : String(Math.round(ops)));

/** The host's use at the latest live tick: CPU split between processes (user) and the kernel, memory and swap, with the cores and load, and what the tick cost telltale. */
export function HostStrip({ host, overhead }: { host: HostUsage; overhead?: Overhead }) {
  return (
    <section aria-label="Host" className="space-y-3">
      <div className="flex flex-wrap gap-3">
        {host.cpuUserPercent !== undefined && <Gauge label="CPU user" value={tenth(host.cpuUserPercent)} tone={toneOf(host.cpuUserPercent)} subtitle="of all cores" />}
        {host.cpuSystemPercent !== undefined && <Gauge label="CPU kernel" value={tenth(host.cpuSystemPercent)} tone={toneOf(host.cpuSystemPercent)} subtitle="of all cores" />}
        <Gauge
          label="Memory"
          value={host.memoryUsedBytes}
          max={host.memoryTotalBytes}
          tone={toneOf(percentOf(host.memoryUsedBytes, host.memoryTotalBytes))}
          subtitle={`${formatBytes(host.memoryUsedBytes)} of ${formatBytes(host.memoryTotalBytes)}`}
        />
        <Gauge
          label="Swap"
          value={host.swapUsedBytes}
          max={host.swapTotalBytes}
          tone={host.swapTotalBytes > 0 ? toneOf(percentOf(host.swapUsedBytes, host.swapTotalBytes)) : "neutral"}
          subtitle={host.swapTotalBytes > 0 ? `${formatBytes(host.swapUsedBytes)} of ${formatBytes(host.swapTotalBytes)}` : "no swap"}
        />
      </div>
      <StatStrip
        columns={overhead ? 10 : 9}
        items={[
          { label: "Cores", value: host.cores },
          { label: "CPU user", value: shareText(host.cpuUserPercent) },
          { label: "CPU kernel", value: shareText(host.cpuSystemPercent) },
          { label: "CPU idle", value: shareText(host.cpuIdlePercent) },
          { label: "Disk read IOPS", value: iopsText(host.diskReadOps) },
          { label: "Disk write IOPS", value: iopsText(host.diskWriteOps) },
          { label: "Disk read/s", value: formatRate(host.diskReadBps) },
          { label: "Disk write/s", value: formatRate(host.diskWriteBps) },
          { label: "Load", value: [host.load1, host.load5, host.load15].map(load => load.toFixed(2)).join(" "), sub: "1 / 5 / 15 min" },
          ...(overhead ? [{ label: "telltale overhead", value: shareText(overhead.cpuPercent), sub: `CPU · ${Math.round(overhead.tickMillis)} ms/tick` }] : []),
        ]}
      />
    </section>
  );
}
