import { getLocale } from "@/lib/i18n";
import type { TrendLineage, TrendMetric } from "@/lib/types/bench";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
	SERIES_SLOTS,
	chartModel,
	formatDate,
	formatMetric,
	isReleaseFlagged,
	latestValuePoint,
	lineageLabel,
	slotColor,
	type PlacedPoint,
	type PlacedSeries,
} from "./trends.utils";

export interface TrendsChartProps {
	lineages: readonly TrendLineage[];
	metric: TrendMetric;
	/** Lineage key -> categorical slot (seriesSlots), so colors follow the lineage, not its rank. */
	slots: Map<string, number>;
}

/**
 * One line per lineage over experiment dates, hand-drawn in SVG (no chart dependency; recharts
 * renders nothing without a DOM, so it could not be tested here). Identity is never color alone:
 * a legend names every lineage, lines past the sixth are dashed, and the markers that matter
 * have their own shapes (a diamond where the config hash changed, a ring where the release was
 * regressed or blocked). The table under the chart carries every number.
 */
export function TrendsChart({ lineages, metric, slots }: TrendsChartProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const titleId = useId();
	const descId = useId();
	const model = useMemo(() => chartModel(lineages, metric, slots), [lineages, metric, slots]);
	const { frame } = model;
	const metricLabel = t(`trends.metric.${metric}`);
	const pointCount = lineages.reduce((n, l) => n + l.points.length, 0);
	const summary = t("trends.chart.summary", { metric: metricLabel, lineages: lineages.length, count: pointCount });
	const latest = lineages
		.map((l) => {
			const p = latestValuePoint(l);
			return p
				? `${lineageLabel(l)}: ${formatMetric(p.value, metric, locale) ?? t("trends.unknown")} (${t(`trends.status.${l.regression.status}`)})`
				: null;
		})
		.filter((s): s is string => s !== null)
		.join("; ");

	return (
		<figure className="flex min-w-0 flex-col gap-3" data-testid="trends-chart">
			<div className="min-w-0 overflow-x-auto">
				<svg
					role="img"
					aria-labelledby={`${titleId} ${descId}`}
					viewBox={`0 0 ${frame.width} ${frame.height}`}
					className="h-auto w-full max-w-4xl min-w-[480px]"
					data-testid="trends-chart-svg"
				>
					<title id={titleId}>{summary}</title>
					<desc id={descId}>{latest ? t("trends.chart.latest", { latest }) : t("trends.chart.noValues")}</desc>
					<Axes model={model} metric={metric} locale={locale} />
					{model.series.map((s) => (
						<Series key={s.lineage.key} series={s} metric={metric} locale={locale} />
					))}
				</svg>
			</div>
			<Legend lineages={lineages} slots={slots} metric={metric} />
		</figure>
	);
}

function Axes({ model, metric, locale }: { model: ReturnType<typeof chartModel>; metric: TrendMetric; locale: string }) {
	const { frame } = model;
	const right = frame.width - frame.right;
	const bottom = frame.height - frame.bottom;
	return (
		<g aria-hidden="true" className="text-muted-foreground" fontSize={11}>
			{model.yTicks.map((tick) => (
				<g key={`y-${tick.value}`}>
					<line x1={frame.left} x2={right} y1={tick.pos} y2={tick.pos} stroke="var(--color-chart-track)" strokeWidth={1} />
					<text x={frame.left - 8} y={tick.pos} dy="0.32em" textAnchor="end" fill="currentColor">
						{formatMetric(tick.value, metric, locale)}
					</text>
				</g>
			))}
			<line x1={frame.left} x2={right} y1={bottom} y2={bottom} stroke="currentColor" strokeOpacity={0.4} strokeWidth={1} />
			{model.xTicks.map((tick, i) => (
				<text
					key={`x-${tick.value}`}
					x={tick.pos}
					y={bottom + 20}
					textAnchor={i === 0 ? "start" : i === model.xTicks.length - 1 ? "end" : "middle"}
					fill="currentColor"
				>
					{formatDate(tick.value, locale)}
				</text>
			))}
		</g>
	);
}

function Series({ series, metric, locale }: { series: PlacedSeries; metric: TrendMetric; locale: string }) {
	const color = slotColor(series.slot);
	const dashed = series.slot >= SERIES_SLOTS;
	return (
		<g data-testid={`trends-series-${series.lineage.key}`} data-slot={series.slot}>
			{series.path && (
				<path
					d={series.path}
					fill="none"
					stroke={color}
					strokeWidth={2}
					strokeLinejoin="round"
					strokeDasharray={dashed ? "6 4" : undefined}
				/>
			)}
			{series.points.map((p, i) => (
				<Marker
					key={`${p.point.experiment}-${p.point.config_hash}-${i}`}
					placed={p}
					lineage={series.lineage}
					color={color}
					metric={metric}
					locale={locale}
				/>
			))}
		</g>
	);
}

/**
 * One point: a circle, or a diamond where the config hash changed, with a ring when the release
 * verdict was regressed or blocked. A point with no value draws nothing on the line; it is in
 * the table. The invisible larger circle is the hover target for the native tooltip.
 */
function Marker({
	placed,
	lineage,
	color,
	metric,
	locale,
}: {
	placed: PlacedPoint;
	lineage: TrendLineage;
	color: string;
	metric: TrendMetric;
	locale: string;
}) {
	const { t } = useTranslation("bench");
	if (placed.y === null) return null;
	const { point, x, y } = placed;
	const flagged = isReleaseFlagged(point);
	const parts = [
		lineageLabel(lineage),
		`${point.experiment} (${formatDate(point.date, locale)}), ${t("trends.table.arm")} ${point.arm}`,
		`${t(`trends.metric.${metric}`)}: ${formatMetric(point.value, metric, locale) ?? t("trends.unknown")}`,
		point.config_changed ? t("trends.chart.configChanged") : null,
		flagged ? t(`trends.release.${point.release as "regressed" | "blocked"}`) : null,
		`${t(`trends.status.${point.regression.status}`)}${point.regression.reason ? `: ${point.regression.reason}` : ""}`,
	].filter(Boolean);
	return (
		<g
			data-testid="trends-point"
			data-config-changed={point.config_changed ? "true" : undefined}
			data-release={flagged ? point.release : undefined}
		>
			<title>{parts.join("\n")}</title>
			{placed.interval && (
				<g stroke={color} strokeWidth={1.5} strokeOpacity={0.6} data-testid="trends-interval">
					<line x1={x} x2={x} y1={placed.interval[0]} y2={placed.interval[1]} />
					<line x1={x - 4} x2={x + 4} y1={placed.interval[0]} y2={placed.interval[0]} />
					<line x1={x - 4} x2={x + 4} y1={placed.interval[1]} y2={placed.interval[1]} />
				</g>
			)}
			{flagged && (
				<circle cx={x} cy={y} r={9} fill="none" stroke="var(--color-chart-error)" strokeWidth={2} data-testid="trends-release-ring" />
			)}
			{point.config_changed ? (
				<rect
					x={x - 5}
					y={y - 5}
					width={10}
					height={10}
					transform={`rotate(45 ${x} ${y})`}
					fill="var(--color-background)"
					stroke={color}
					strokeWidth={2}
					data-testid="trends-config-marker"
				/>
			) : (
				<circle cx={x} cy={y} r={4} fill={color} stroke="var(--color-background)" strokeWidth={2} />
			)}
			<circle cx={x} cy={y} r={12} fill="transparent" />
		</g>
	);
}

/** Lineage names with their line, then the marker key. Text stays in text colors; the swatch carries identity. */
function Legend({ lineages, slots, metric }: { lineages: readonly TrendLineage[]; slots: Map<string, number>; metric: TrendMetric }) {
	const { t } = useTranslation("bench");
	return (
		<figcaption className="text-muted-foreground flex flex-col gap-2 text-xs" data-testid="trends-legend">
			<ul className="flex flex-wrap gap-x-4 gap-y-1">
				{lineages.map((l) => {
					const slot = slots.get(l.key) ?? 0;
					return (
						<li key={l.key} className="text-foreground flex items-center gap-1.5" data-testid={`trends-legend-${l.key}`}>
							<svg width={20} height={8} aria-hidden="true">
								<line
									x1={0}
									x2={20}
									y1={4}
									y2={4}
									stroke={slotColor(slot)}
									strokeWidth={2}
									strokeDasharray={slot >= SERIES_SLOTS ? "6 4" : undefined}
								/>
							</svg>
							{lineageLabel(l)}
						</li>
					);
				})}
			</ul>
			<ul className="flex flex-wrap gap-x-4 gap-y-1">
				<li className="flex items-center gap-1.5">
					<svg width={14} height={14} aria-hidden="true">
						<rect x={3} y={3} width={8} height={8} transform="rotate(45 7 7)" fill="none" stroke="currentColor" strokeWidth={1.5} />
					</svg>
					{t("trends.chart.legendConfigChanged")}
				</li>
				<li className="flex items-center gap-1.5">
					<svg width={14} height={14} aria-hidden="true">
						<circle cx={7} cy={7} r={5.5} fill="none" stroke="var(--color-chart-error)" strokeWidth={1.5} />
					</svg>
					{t("trends.chart.legendRelease")}
				</li>
				{metric === "verified_success" && (
					<li className="flex items-center gap-1.5">
						<svg width={14} height={14} aria-hidden="true">
							<line x1={7} x2={7} y1={2} y2={12} stroke="currentColor" strokeWidth={1.5} />
						</svg>
						{t("trends.chart.legendInterval")}
					</li>
				)}
			</ul>
		</figcaption>
	);
}