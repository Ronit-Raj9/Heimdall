import { ValueCell } from "@/components/valueCell";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Value, ValueKind } from "@/lib/types/bench";
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
	return (
		<Table data-testid="run-metric-strip">
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
										<span className="inline-flex items-baseline gap-2">
											<ValueCell value={value} kind={kinds[name]} testId={`run-metric-${name}-${arm}`} />
											{value?.evidence && <span className="text-muted-foreground font-mono text-[10px]">{value.evidence}</span>}
										</span>
									</TableCell>
								);
							})}
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}