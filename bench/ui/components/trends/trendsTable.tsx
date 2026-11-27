import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { TrendLineage, TrendMetric, TrendPoint } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ReleaseFlag, RegressionBadge } from "./trendsBadges";
import { formatDate, formatMetric, lineageLabel, shortHash } from "./trends.utils";

type MetricKey = Exclude<TrendMetric, "verified_success">;
/** The metric columns after success, in order; the chart's own metric is one of them, so no extra value column. */
const METRIC_KEYS: readonly MetricKey[] = ["cost_per_success", "tokens", "wall_time_s", "false_completion"];
/** Metric columns shown before "More columns" is pressed; the rest wait behind it. */
const COMPACT_METRICS: readonly MetricKey[] = ["cost_per_success"];
const METRIC_HEADS = {
	cost_per_success: "trends.table.costPerSuccess",
	tokens: "trends.table.tokens",
	wall_time_s: "trends.table.wallTime",
	false_completion: "trends.table.falseCompletion",
} as const;

export interface TrendsTableProps {
	lineages: readonly TrendLineage[];
	/** The chart's metric; its numbers are in their own column, so the table needs no extra one. */
	metric: TrendMetric;
	/** Start with every column shown (tests and print); the page starts compact. */
	initialMore?: boolean;
}

/**
 * Every point of every lineage, oldest first within a lineage: the chart's table view. Numbers
 * that were not observed say so and are never shown as zero.
 */
export function TrendsTable({ lineages, initialMore = false }: TrendsTableProps) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u12");
	const [more, setMore] = useState(initialMore);
	const points = lineages.flatMap((l) => l.points);
	// A metric column that no point carries is left out until some point has it (the chart's own metric is one of these).
	const has = (key: MetricKey) => points.some((p) => p[key] !== null && p[key] !== undefined);
	const metrics = METRIC_KEYS.filter((k) => has(k) && (more || COMPACT_METRICS.includes(k)));
	const head = [
		t("trends.table.lineage"),
		t("trends.table.experiment"),
		t("trends.table.date"),
		...(more ? [t("trends.table.arm"), t("trends.table.config")] : []),
		t("trends.table.success"),
		...metrics.map((k) => t(METRIC_HEADS[k])),
		t("trends.table.verdict"),
		t("trends.table.regression"),
	];
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<div className="flex justify-end">
				<Button variant="ghost" size="sm" onClick={() => setMore((m) => !m)} dataTestId="trends-columns-toggle">
					{more ? tu("board.less") : tu("board.more")}
				</Button>
			</div>
			<div className="min-w-0 overflow-x-auto rounded-sm border">
				<table className="w-full text-left text-xs" data-testid="trends-table">
					<caption className="sr-only">{t("trends.table.caption")}</caption>
					<thead className="bg-muted/50 text-muted-foreground">
						<tr>
							{head.map((h, i) => (
								<th key={`col-${i}`} scope="col" className="px-3 py-2 font-medium whitespace-nowrap">
									{h}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{lineages.flatMap((l) =>
							l.points.map((p, i) => (
								<PointRow key={`${l.key}-${p.experiment}-${p.config_hash}-${i}`} lineage={l} point={p} metrics={metrics} first={i === 0} more={more} />
							)),
						)}
					</tbody>
				</table>
			</div>
		</div>
	);
}

function PointRow({
	lineage,
	point,
	metrics,
	first,
	more,
}: { lineage: TrendLineage; point: TrendPoint; metrics: readonly MetricKey[]; first: boolean; more: boolean }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const fmt = (v: number | null, m: TrendMetric) => formatMetric(v, m, locale) ?? t("trends.unknown");
	const success = formatMetric(point.success, "verified_success", locale);
	const lo = formatMetric(point.success_low, "verified_success", locale);
	const hi = formatMetric(point.success_high, "verified_success", locale);
	return (
		<tr className={cn("h-10 border-t align-middle", first && "border-t-2")} data-testid="trends-row">
			<th scope="row" className="text-foreground px-3 py-2 font-medium whitespace-nowrap">
				{first ? lineageLabel(lineage) : <span className="sr-only">{lineageLabel(lineage)}</span>}
			</th>
			<td className="px-3 py-2 font-mono">
				<span className="block max-w-48 truncate" title={point.experiment}>
					{point.experiment}
				</span>
			</td>
			<td className="px-3 py-2 whitespace-nowrap">{formatDate(point.date, locale)}</td>
			{more && <td className="px-3 py-2 font-mono">{point.arm}</td>}
			{more && (
				<td className="px-3 py-2 font-mono whitespace-nowrap" title={point.config_hash}>
					{shortHash(point.config_hash)}
					{point.config_changed && (
						<span className="text-foreground ml-1.5 font-sans" data-testid="trends-config-changed">
							{t("trends.table.configChanged")}
						</span>
					)}
				</td>
			)}
			<td className="px-3 py-2 font-mono whitespace-nowrap">
				{success ?? t("trends.unknown")}
				{lo && hi && <span className="text-muted-foreground ml-1">{t("trends.table.interval", { lo, hi })}</span>}
			</td>
			{metrics.map((k) => (
				<td key={k} className="px-3 py-2 text-right font-mono tabular-nums whitespace-nowrap">
					{fmt(point[k], k)}
				</td>
			))}
			<td className="px-3 py-2">
				<div className="flex items-center gap-2 whitespace-nowrap">
					<span title={point.policy ? t("trends.table.policy", { decision: point.policy }) : undefined}>
						{point.decision || t("trends.table.noAnalysis")}
					</span>
					{more && point.policy && <span className="text-muted-foreground">{t("trends.table.policy", { decision: point.policy })}</span>}
					<ReleaseFlag point={point} testId="trends-release-flag" />
				</div>
			</td>
			<td className="px-3 py-2">
				<div className="flex max-w-80 items-center gap-2">
					<RegressionBadge regression={point.regression} />
					{more && point.regression.reason && (
						<span className="text-muted-foreground block max-w-full truncate" title={point.regression.reason}>
							{point.regression.reason}
						</span>
					)}
				</div>
			</td>
		</tr>
	);
}