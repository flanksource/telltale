import { Sparkline, type SampleFrame } from "@flanksource/clicky-ui/data";

type Metric = "cpuPercent" | "readIOPS" | "writeIOPS" | "pageLifeExpectancySeconds" | "bufferUsagePercent";
type ServerMetrics = Partial<Record<Metric, number>> & { unavailable?: Partial<Record<Metric, string>> };
export type SQLTopSample = SampleFrame & { summary?: { engine?: string; serverMetrics?: ServerMetrics } };

const metrics: Record<Metric, { label: string; unit: string }> = {
  cpuPercent: { label: "Host CPU", unit: "%" },
  readIOPS: { label: "Read IOPS", unit: " ops/s" },
  writeIOPS: { label: "Write IOPS", unit: " ops/s" },
  pageLifeExpectancySeconds: { label: "Page life expectancy", unit: " s" },
  bufferUsagePercent: { label: "Shared buffers used", unit: "%" },
};

function trend(frames: SQLTopSample[], metric: Metric): number[] {
  const values: number[] = [];
  for (let i = frames.length - 1; i >= 0 && values.length < 30; i--) {
    const value = frames[i]?.summary?.serverMetrics?.[metric];
    if (value === undefined) break;
    values.push(value);
  }
  return values.reverse();
}

export function SQLTopServerMetrics({ frame, frames }: { frame: SQLTopSample; frames: SQLTopSample[] }) {
  const summary = frame.summary;
  const reading = summary?.serverMetrics;
  if (!reading) return null;
  const sqlServer = summary.engine === "sqlserver";
  const keys: Metric[] = ["cpuPercent", "readIOPS", "writeIOPS", sqlServer ? "pageLifeExpectancySeconds" : "bufferUsagePercent"];
  const source = frames.filter(sample => sample.source === frame.source);
  const index = source.findIndex(sample => sample.id === frame.id);
  const history = index < 0 ? [frame] : source.slice(Math.max(0, index - 29), index + 1);
  return (
    <section aria-label="Database server metrics" className="grid shrink-0 grid-cols-2 gap-2 sm:grid-cols-4">
      {keys.map(key => {
        const { label: defaultLabel, unit } = metrics[key];
        const label = sqlServer && key === "cpuPercent" ? "SQL Server CPU" : defaultLabel;
        const value = reading[key];
        const values = trend(history, key);
        return (
          <div key={key} className="min-w-0 rounded-lg border border-border bg-card px-3 py-2">
            <div className="text-xs text-muted-foreground">{label}</div>
            {value === undefined ? (
              <div className="mt-1 text-xs text-muted-foreground">{reading.unavailable?.[key] ?? "Unavailable"}</div>
            ) : (
              <div className="mt-1 text-sm font-semibold tabular-nums">{value.toLocaleString(undefined, { maximumFractionDigits: 1 })}{unit}</div>
            )}
            {values.length > 1 && (
              <div role="group" aria-label={`${label} over time`} className="mt-1">
                <Sparkline values={values} width={128} height={18} className="max-w-full" />
              </div>
            )}
          </div>
        );
      })}
    </section>
  );
}
