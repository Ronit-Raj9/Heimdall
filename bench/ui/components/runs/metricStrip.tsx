import { ValueCell } from "@/components/valueCell";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Value, ValueKind } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { STRIP_METRICS, type StripMetricName } from "./runs.utils";

export interface MetricStripProps {
	lanes: readonly { arm: string; metrics: Record<string, Value> }[];
	kinds: Record<StripMetricName, ValueKind>;
}

/** Steps, tool calls, tokens, cost, wall time and verified success of each run, side by side, through ValueCell. */
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
				{STRIP_METRICS.map(({ name }) => (
					<TableRow key={name} data-testid={`run-metric-${name}`}>
						<TableCell className="text-muted-foreground">{t(`runs.metrics.${name}`)}</TableCell>
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
				))}
			</TableBody>
		</Table>
	);
}