import { AgentCell, ReliabilityCell, SuccessCell, TierBadge } from "@/components/leaderboard/leaderboardCells";
import { tierOf } from "@/components/leaderboard/leaderboard.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import type { MatrixRow, ValueKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

/** Metric columns of the matrix, in order: the leaderboard's ids, so the same header words apply. */
export const MATRIX_METRIC_COLUMNS = [
	{ id: "cost_per_success", kind: "usd" },
	{ id: "tokens", kind: "tokens" },
	{ id: "wall_time_s", kind: "duration" },
	{ id: "false_completion", kind: "rate" },
] as const satisfies readonly { id: string; kind: ValueKind }[];

/** One row per arm in the server's rank order. Cells are the leaderboard's, so unknowns are hatched, never 0. */
export function MatrixTable({ rows }: { rows: readonly MatrixRow[] }) {
	const { t } = useTranslation("bench");
	return (
		<Table stickyColumns={4} data-testid="matrix-table" aria-label={t("matrix.table.label")} containerClassName="rounded-sm">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					<TableHead>{t("leaderboard.table.rank")}</TableHead>
					<TableHead>{t("leaderboard.table.tier")}</TableHead>
					<TableHead>{t("matrix.table.arm")}</TableHead>
					<TableHead>{t("leaderboard.table.agent")}</TableHead>
					<TableHead>{t("matrix.table.provider")}</TableHead>
					<TableHead title={t("leaderboard.table.successHint")}>{t("leaderboard.table.success")}</TableHead>
					<TableHead className="text-right" title={t("leaderboard.table.reliabilityHint")}>
						{t("leaderboard.table.reliability")}
					</TableHead>
					{MATRIX_METRIC_COLUMNS.map((column) => (
						<TableHead key={column.id} className="text-right" title={t(`leaderboard.table.${column.id}Hint`)}>
							{t(`leaderboard.table.${column.id}`)}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => {
					const tier = tierOf(row);
					return (
						<TableRow key={row.arm} data-testid={`matrix-row-${row.arm}`} data-tier={tier} data-rank={row.rank}>
							<TableCell
								className={cn(
									"text-foreground align-top font-semibold tabular-nums",
									tier === 1 ? "shadow-[inset_3px_0_0_var(--chart-success)]" : "shadow-[inset_3px_0_0_var(--chart-neutral)]",
								)}
							>
								{row.rank}
							</TableCell>
							<TableCell className="align-top">
								<TierBadge entry={row} />
							</TableCell>
							<TableCell className="align-top font-mono font-semibold" data-testid={`matrix-arm-${row.arm}`}>
								{row.arm}
							</TableCell>
							<TableCell className="align-top">
								<AgentCell entry={row} />
							</TableCell>
							<TableCell className="text-muted-foreground align-top font-mono text-xs" data-testid={`matrix-provider-${row.arm}`}>
								{row.provider}
							</TableCell>
							<TableCell className="align-top">
								<SuccessCell entry={row} />
							</TableCell>
							<TableCell className="text-right align-top">
								<ReliabilityCell entry={row} />
							</TableCell>
							{MATRIX_METRIC_COLUMNS.map((column) => (
								<TableCell key={column.id} className="text-right align-top" data-testid={`matrix-metric-${row.arm}-${column.id}`}>
									<ValueCell value={row.metrics?.[column.id]} kind={column.kind} />
								</TableCell>
							))}
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}