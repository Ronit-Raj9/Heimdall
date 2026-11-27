import { ValueCell } from "@/components/valueCell";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Value, ValueKind } from "@/lib/types/bench";
import "@/app/leaderboard/views/uf4bI18n";
import { FoldedRows } from "@/components/ui/facts";
import { cn } from "@/lib/utils";
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
	const metrics = visibleStripMetrics(lanes);
	// Round 3 rule 9: a measurement no setup recorded folds behind one line instead of a row of "unknown".
	const recorded = metrics.filter((m) => !notRecorded(lanes, m.name));
	const missing = metrics.filter((m) => notRecorded(lanes, m.name));
	// Rule 7: label and values stay close; the table grows with the number of setups, not the page.
	const cap = lanes.length <= 1 ? "max-w-[40rem]" : lanes.length <= 3 ? "max-w-[56rem]" : undefined;
	const table = (rows: typeof metrics, testId?: string) => (
		<Table data-testid={testId} stickyColumns={1}>
			<TableHeader>
				<TableRow>
					<TableHead>{t("runs.metrics.metric")}</TableHead>
					{lanes.map(({ arm }) => (
						<TableHead key={`head-${arm}`} numeric>
							{t("runs.arm", { arm })}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((metric) => {
					const { name } = metric;
					const family = "family" in metric && metric.family;
					return (
						<TableRow key={name} data-testid={`run-metric-${name}`}>
							<TableCell className={family ? "text-muted-foreground pl-6 text-xs" : "text-muted-foreground"}>
								<span title={t(`runs.metrics.help.${name}`)} data-testid={`run-metric-help-${name}`}>
									{t(`runs.metrics.${name}`)}
								</span>
							</TableCell>
							{lanes.map(({ arm, metrics }) => (
								<TableCell key={`${name}-${arm}`} numeric>
									<ValueCell value={metrics[name]} kind={kinds[name]} testId={`run-metric-${name}-${arm}`} />
								</TableCell>
							))}
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
	return (
		<div className={cn("flex min-w-0 flex-col gap-1", cap)}>
			{recorded.length > 0 && table(recorded, "run-metric-strip")}
			<FoldedRows count={missing.length} className={recorded.length > 0 ? "px-4 pb-2" : "px-4 py-2"} testId="run-metric-folded">
				{table(missing, recorded.length > 0 ? undefined : "run-metric-strip")}
			</FoldedRows>
			{sources.length > 0 && (
				<p className="text-muted-foreground px-4 pb-2 text-xs" data-testid="run-metric-sources">
					{tu("metrics.sources", { list: sources.join(", ") })}
				</p>
			)}
		</div>
	);
}

/** True when no setup recorded this measurement (every lane's value is missing or unknown without bounds). Pure. */
export function notRecorded(lanes: MetricStripProps["lanes"], name: string): boolean {
	return lanes.every(({ metrics }) => {
		const v = metrics[name];
		return !v || (v.state === "unknown" && !v.bounds);
	});
}

/** The distinct evidence sources of every shown number, in first-seen order. Pure. */
export function metricSources(lanes: MetricStripProps["lanes"]): string[] {
	const out: string[] = [];
	for (const { metrics } of lanes) for (const v of Object.values(metrics)) if (v?.evidence && !out.includes(v.evidence)) out.push(v.evidence);
	return out;
}