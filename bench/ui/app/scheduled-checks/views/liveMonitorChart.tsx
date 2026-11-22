import type { MonitorSample } from "@/lib/store/apis/liveMonitorsApi";

/** One numeric series over check times; null values leave a gap. */
export interface ChartSeries {
	label: string;
	values: (number | null)[];
	dashed?: boolean;
}

/** Points of one series in a W x H box, x by index, y scaled to [0, max]. Exported for tests. */
export function seriesPath(values: (number | null)[], max: number, w: number, h: number): string {
	const n = values.length;
	let d = "";
	let pen = false;
	values.forEach((v, i) => {
		if (v === null || !Number.isFinite(v)) {
			pen = false;
			return;
		}
		const x = n <= 1 ? w / 2 : (i / (n - 1)) * w;
		const y = h - (max > 0 ? Math.min(v / max, 1) : 0) * h;
		d += `${pen ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
		pen = true;
	});
	return d;
}

/** The pass rate of one check, null when no run was graded. */
export function samplePassRate(s: MonitorSample): number | null {
	return s.scored > 0 ? s.passed / s.scored : null;
}

/** A small hand-drawn SVG line chart (no chart dependency, renders in tests). */
export function LineChart({ title, series, max, format, testId }: { title: string; series: ChartSeries[]; max?: number; format: (v: number) => string; testId: string }) {
	const w = 320;
	const h = 80;
	const all = series.flatMap((s) => s.values.filter((v): v is number => v !== null && Number.isFinite(v)));
	const top = max ?? (all.length ? Math.max(...all) * 1.1 || 1 : 1);
	const last = series.map((s) => [...s.values].reverse().find((v) => v !== null && Number.isFinite(v)) ?? null);
	return (
		<figure className="flex min-w-0 flex-col gap-1 rounded-md border p-3" data-testid={testId}>
			<figcaption className="flex flex-wrap items-baseline justify-between gap-2 text-xs">
				<span className="font-medium">{title}</span>
				<span className="text-muted-foreground">
					{series.map((s, i) => `${s.label} ${last[i] === null ? "-" : format(last[i] as number)}`).join(" · ")}
				</span>
			</figcaption>
			<svg viewBox={`0 0 ${w} ${h}`} className="h-20 w-full" preserveAspectRatio="none" role="img" aria-label={title}>
				<line x1={0} y1={h} x2={w} y2={h} className="stroke-border" strokeWidth={1} />
				{series.map((s, i) => (
					<path
						key={s.label}
						d={seriesPath(s.values, top, w, h)}
						fill="none"
						className={i === 0 ? "stroke-primary" : "stroke-muted-foreground"}
						strokeWidth={1.5}
						strokeDasharray={s.dashed ? "4 3" : undefined}
						vectorEffect="non-scaling-stroke"
					/>
				))}
			</svg>
		</figure>
	);
}
