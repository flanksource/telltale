import { topCpuSeries } from "./groups";
import type { Frame } from "./api";

const SERIES_COLORS = ["#2563eb", "#dc2626", "#16a34a", "#9333ea", "#ea580c", "#0891b2", "#ca8a04", "#db2777"];

const CHART = { width: 640, height: 180, left: 44, right: 8, top: 8, bottom: 20 };

/** The total CPU % of the groups that peaked highest over the ticks, one line each. */
export function CpuChart({ frames, count = 5 }: { frames: Frame[]; count?: number }) {
  const series = topCpuSeries(frames, count);
  if (frames.length < 2 || series.length === 0) return <p className="text-sm text-muted-foreground">A chart needs at least two ticks.</p>;
  const peak = Math.max(1, ...series.flatMap(line => line.values.map(value => value ?? 0)));
  const plotWidth = CHART.width - CHART.left - CHART.right;
  const plotHeight = CHART.height - CHART.top - CHART.bottom;
  const x = (index: number) => CHART.left + (index / (frames.length - 1)) * plotWidth;
  const y = (value: number) => CHART.top + plotHeight - (value / peak) * plotHeight;
  return (
    <figure className="space-y-2">
      <svg role="img" aria-label={`Total CPU % of the ${series.length} busiest groups over ${frames.length} ticks`} viewBox={`0 0 ${CHART.width} ${CHART.height}`} className="w-full max-w-3xl text-muted-foreground">
        <line x1={CHART.left} x2={CHART.width - CHART.right} y1={y(0)} y2={y(0)} stroke="currentColor" strokeOpacity={0.4} />
        <line x1={CHART.left} x2={CHART.width - CHART.right} y1={y(peak)} y2={y(peak)} stroke="currentColor" strokeOpacity={0.2} strokeDasharray="3 3" />
        <text x={CHART.left - 6} y={y(peak) + 4} textAnchor="end" fontSize={11} fill="currentColor">{peak.toFixed(0)}%</text>
        <text x={CHART.left - 6} y={y(0) + 4} textAnchor="end" fontSize={11} fill="currentColor">0%</text>
        <text x={CHART.left} y={CHART.height - 4} fontSize={11} fill="currentColor">tick {frames[0]!.tick}</text>
        <text x={CHART.width - CHART.right} y={CHART.height - 4} textAnchor="end" fontSize={11} fill="currentColor">tick {frames.at(-1)!.tick}</text>
        {series.map((line, lineIndex) => (
          <polyline
            key={line.key}
            data-series={line.name}
            fill="none"
            stroke={SERIES_COLORS[lineIndex % SERIES_COLORS.length]}
            strokeWidth={1.75}
            strokeLinejoin="round"
            points={line.values.flatMap((value, index) => value === null ? [] : [`${x(index).toFixed(1)},${y(value).toFixed(1)}`]).join(" ")}
          />
        ))}
      </svg>
      <figcaption className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
        {series.map((line, index) => (
          <span key={line.key} className="flex items-center gap-1">
            <span aria-hidden className="inline-block h-2 w-3 rounded-sm" style={{ background: SERIES_COLORS[index % SERIES_COLORS.length] }} />
            {line.name}
          </span>
        ))}
      </figcaption>
    </figure>
  );
}
