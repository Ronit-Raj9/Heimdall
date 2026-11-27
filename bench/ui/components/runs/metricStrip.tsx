import { ValueCell } from "@/components/valueCell";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Value, ValueKind } from "@/lib/types/bench";
import "@/app/leaderboard/views/uf4bI18n";
import { useTranslation } from "react-i18next";
import { type StripMetricName, visibleStripMetrics } from "./runs.utils";

export interface MetricStripProps {
	lanes: readonly { arm: string; metrics: Record<string, Value> }[];
	kinds: Record<StripMetricName, ValueKind>;
}

/**
 * Steps, tool calls, tokens, cost, wall time and verified success of each run, side by side, through ValueCell. Steps carries
 * its family under it (failed turns, attempts, agent steps) and tool calls its proof classes, each with the help text that says
 * what it counts, so no one number is read as everything the agent did.
 */
export function MetricStrip({ lanes, kinds }: MetricStripProps) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4b");
	// M4-58: one legend line names where the numbers come from, instead of a tag in every cell.
	const sources = metricSources(lanes);
	return (
		<div className="flex min-w-0 flex-col gap-1">
		<Table data-testid="run-metric-strip" stickyColumns={1}>
			<TableHeader>
				<TableRow>
					<TableHead>{t("runs.metrics.metric")}</TableHead>
					{lanes.map(({ arm }) => (
						<TableHead key={`head-${arm}`} className="text-right">
							{t("runs.arm", { arm })}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{visibleStripMetrics(lanes).map((metric) => {
					const { name } = metric;
					const family = "family" in metric && metric.family;
					return (
						<TableRow key={name} data-testid={`run-metric-${name}`}>
							<TableCell className={family ? "text-muted-foreground pl-6 text-xs" : "text-muted-foreground"}>
								<span title={t(`runs.metrics.help.${name}`)} data-testid={`run-metric-help-${name}`}>
									{t(`runs.metrics.${name}`)}
								</span>
							</TableCell>
							{lanes.map(({ arm, metrics }) => {
								const value = metrics[name];
								return (
									<TableCell key={`${name}-${arm}`} className="text-right">
										<ValueCell value={value} kind={kinds[name]} testId={`run-metric-${name}-${arm}`} />
									</TableCell>
								);
							})}
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
		{sources.length > 0 && (
			<p className="text-muted-foreground text-xs" data-testid="run-metric-sources">
				{tu("metrics.sources", { list: sources.join(", ") })}
			</p>
		)}
		</div>
	);
}

/** The distinct evidence sources of every shown number, in first-seen order. Pure. */
export function metricSources(lanes: MetricStripProps["lanes"]): string[] {
	const out: string[] = [];
	for (const { metrics } of lanes) for (const v of Object.values(metrics)) if (v?.evidence && !out.includes(v.evidence)) out.push(v.evidence);
	return out;
}