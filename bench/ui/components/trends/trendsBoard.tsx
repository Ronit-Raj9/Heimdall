import { EmptyNote, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { Trends } from "@/lib/types/bench";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { RegressionBadge } from "./trendsBadges";
import { TrendsChart } from "./trendsChart";
import { TrendsTable } from "./trendsTable";
import { MIN_TREND_POINTS, formatDate, lineageLabel, seriesSlots, totalPoints } from "./trends.utils";

export interface TrendsBoardProps {
	data: Trends;
	/** Offered from the empty state of a live-only response: synthetic runs may exist. */
	onIncludeSynthetic?: () => void;
}

/**
 * The trends body: each lineage's newest regression flag, the chart and its table, and the
 * footnote that says where the numbers come from. Below MIN_TREND_POINTS points there is no
 * line to draw, so it shows an empty state instead.
 */
export function TrendsBoard({ data, onIncludeSynthetic }: TrendsBoardProps) {
	const { t } = useTranslation("bench");
	const lineages = data.lineages ?? [];
	const slots = useMemo(() => seriesSlots(lineages, data.agents ?? []), [lineages, data.agents]);
	const points = totalPoints({ points: data.points, lineages });

	if (points < MIN_TREND_POINTS) {
		return (
			<div className="flex flex-col gap-3" data-testid="trends-empty">
				<EmptyNote testId="trends-empty-note">{t("trends.empty", { count: points })}</EmptyNote>
				{data.synthetic === "exclude" && onIncludeSynthetic && (
					<Button variant="outline" size="sm" className="self-center" onClick={onIncludeSynthetic} dataTestId="trends-include-synthetic">
						{t("trends.includeSynthetic")}
					</Button>
				)}
				<Footnote data={data} />
			</div>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-6">
			<Section
				title={t("trends.lineages.title")}
				description={t("trends.lineages.description", { alpha: data.alpha })}
				testId="trends-lineages"
			>
				<ul className="flex flex-col gap-2">
					{lineages.map((l) => (
						<li key={l.key} className="flex flex-wrap items-center gap-2 text-sm" data-testid={`trends-lineage-${l.key}`}>
							<span className="text-foreground font-medium">{lineageLabel(l)}</span>
							<RegressionBadge regression={l.regression} testId="trends-lineage-badge" />
							<span className="text-muted-foreground text-xs">
								{l.regression.reason || t("trends.lineages.points", { count: l.points.length })}
							</span>
						</li>
					))}
				</ul>
			</Section>
			<Section
				title={t("trends.chart.title", { metric: t(`trends.metric.${data.metric}`) })}
				description={t("trends.chart.description")}
				testId="trends-chart-section"
			>
				<TrendsChart lineages={lineages} metric={data.metric} slots={slots} />
			</Section>
			<Section title={t("trends.table.title")} description={t("trends.table.description")} testId="trends-table-section">
				<TrendsTable lineages={lineages} metric={data.metric} />
			</Section>
			<Footnote data={data} />
		</div>
	);
}

/** Source of the numbers: the derived index, its age, the rejected runs, and that a trend never gates a release. */
function Footnote({ data }: { data: Trends }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<div className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="trends-footnote">
			<p>{t("trends.footnote.source")}</p>
			{data.indexed_at && <p>{t("trends.footnote.indexedAt", { time: formatDate(data.indexed_at, locale) })}</p>}
			{data.rejected > 0 && <p data-testid="trends-rejected">{t("trends.footnote.rejected", { count: data.rejected })}</p>}
		</div>
	);
}