import type { ControlBand, CurvePoint } from "@/lib/types/improve";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { SERIES, curveModel, pct } from "./improve.utils";

export interface ImprovementCurveProps {
	curve: readonly CurvePoint[];
	band?: ControlBand;
	rounds: number;
}

const markerColor: Record<string, string> = {
	committed: "var(--color-chart-success)",
	rejected: "var(--color-chart-neutral)",
	tripped: "var(--color-chart-error)",
	pending: "var(--color-chart-info)",
};

/**
 * Rounds on x, success on y: practice, gate and held-out lines for the kept version, the band
 * version 0 reaches with the same spend, and the no-feedback control as a dotted line. Lines differ
 * by dash as well as color, and the table under the chart carries every number.
 */
export function ImprovementCurve({ curve, band, rounds }: ImprovementCurveProps) {
	const { t } = useTranslation("improve");
	const titleId = useId();
	const m = useMemo(() => curveModel(curve, band, rounds), [curve, band, rounds]);
	const { frame } = m;
	const right = frame.width - frame.right;
	const bottom = frame.height - frame.bottom;
	return (
		<figure className="flex min-w-0 flex-col gap-3" data-testid="improvement-curve">
			<div className="min-w-0 overflow-x-auto">
				<svg role="img" aria-labelledby={titleId} viewBox={`0 0 ${frame.width} ${frame.height}`} className="text-muted-foreground h-auto w-full max-w-3xl min-w-[420px] text-[11px]">
					<title id={titleId}>{t("curve.summary", { count: Math.max(0, curve.length - 1) })}</title>
					{[0, 0.25, 0.5, 0.75, 1].map((v) => (
						<g key={v}>
							<line x1={frame.left} x2={right} y1={m.y(v)} y2={m.y(v)} stroke="var(--color-chart-track)" strokeWidth={1} />
							<text x={frame.left - 8} y={m.y(v)} dy="0.32em" textAnchor="end" fill="currentColor">
								{pct(v)}
							</text>
						</g>
					))}
					{m.band && (
						<rect
							x={m.band.x1}
							width={m.band.x2 - m.band.x1}
							y={m.band.y1}
							height={Math.max(1, m.band.y2 - m.band.y1)}
							fill="var(--color-chart-neutral)"
							fillOpacity={0.14}
							data-testid="improvement-band"
						/>
					)}
					{m.nullY !== undefined && (
						<line x1={frame.left} x2={right} y1={m.nullY} y2={m.nullY} stroke="var(--color-chart-neutral)" strokeDasharray="2 3" strokeWidth={1.5} data-testid="improvement-null" />
					)}
					{m.rounds.map((r) => (
						<text key={r} x={m.x(r)} y={bottom + 18} textAnchor="middle" fill="currentColor">
							{r}
						</text>
					))}
					{m.markers.map((k) => (
						<line key={k.round} x1={k.x} x2={k.x} y1={frame.top} y2={bottom} stroke={markerColor[k.event] ?? "var(--color-chart-neutral)"} strokeOpacity={0.5} strokeWidth={k.event === "committed" ? 2 : 1} data-testid={`improvement-marker-${k.event}`} />
					))}
					{m.lines.map((l) => {
						const s = SERIES.find((x) => x.key === l.key)!;
						return (
							<g key={l.key} data-testid={`improvement-line-${l.key}`}>
								<path d={l.path} fill="none" stroke={s.color} strokeWidth={2} strokeDasharray={s.dash} strokeLinejoin="round" />
								{l.points.map((p) => (
									<circle key={p.round} cx={p.x} cy={p.y} r={3.5} fill={s.color} stroke="var(--color-background)" strokeWidth={1.5} />
								))}
							</g>
						);
					})}
				</svg>
			</div>
			<figcaption className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
				{SERIES.map((s) => (
					<span key={s.key} className="inline-flex items-center gap-1.5">
						<svg width="18" height="6" aria-hidden="true">
							<line x1="0" x2="18" y1="3" y2="3" stroke={s.color} strokeWidth="2" strokeDasharray={s.dash} />
						</svg>
						{t(`split.${s.key}`)}
					</span>
				))}
				{band && band.trials > 0 && (
					<span className="inline-flex items-center gap-1.5">
						<span className="bg-chart-neutral/20 inline-block h-2.5 w-4 rounded-sm" aria-hidden="true" />
						{t("curve.band")} ({pct(band.low)} to {pct(band.high)})
					</span>
				)}
				{band?.null_mean !== undefined && (
					<span className="inline-flex items-center gap-1.5">
						<svg width="18" height="6" aria-hidden="true">
							<line x1="0" x2="18" y1="3" y2="3" stroke="var(--color-chart-neutral)" strokeWidth="1.5" strokeDasharray="2 3" />
						</svg>
						{t("curve.null")} ({pct(band.null_mean)})
					</span>
				)}
			</figcaption>
			<table className="text-muted-foreground w-full max-w-3xl text-left text-xs tabular-nums" data-testid="improvement-curve-table">
				<thead>
					<tr className="border-b">
						<th className="py-1 font-medium" />
						{SERIES.map((s) => (
							<th key={s.key} className="py-1 font-medium">
								{t(`split.${s.key}`)}
							</th>
						))}
						<th className="py-1 font-medium" />
					</tr>
				</thead>
				<tbody>
					{curve.map((p) => (
						<tr key={p.round} className="border-b last:border-0">
							<td className="py-1">{t("curve.round", { n: p.round })}</td>
							<td className="text-foreground py-1">{pct(p.dev)}</td>
							<td className="text-foreground py-1">{pct(p.audit)}</td>
							<td className="text-foreground py-1">{pct(p.sealed)}</td>
							<td className="py-1">{t(`curve.event.${p.event}`)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</figure>
	);
}
