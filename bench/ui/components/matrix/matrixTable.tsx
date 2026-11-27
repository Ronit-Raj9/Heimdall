import { AgentCell, ReliabilityCell, SuccessCell, TierBadge } from "@/components/leaderboard/leaderboardCells";
import { observedNumber, tierOf } from "@/components/leaderboard/leaderboard.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import type { MatrixRow, ValueKind } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import "./ufc3I18n";

/** Metric columns of the matrix, in order: the leaderboard's ids, so the same header words apply. */
export const MATRIX_METRIC_COLUMNS = [
	{ id: "cost_per_success", kind: "usd" },
	{ id: "tokens", kind: "tokens" },
	{ id: "wall_time_s", kind: "duration" },
	{ id: "false_completion", kind: "rate" },
] as const satisfies readonly { id: string; kind: ValueKind }[];

/** A row with the rank it prints: "=1" for each setup tied with the leader when two or more are (M4-48). */
export type RankedMatrixRow = { row: MatrixRow; rank: string; tied: boolean };

/**
 * The rows in print order (M4-48): the setups tied with the leader first, cheapest per solved task
 * first (an unknown cost last), each ranked "=1" when two or more are tied; the rest keep the
 * server's order and rank. Pure.
 */
export function rankMatrixRows(rows: readonly MatrixRow[]): RankedMatrixRow[] {
	const top = rows.filter((r) => tierOf(r) === 1);
	const rest = rows.filter((r) => tierOf(r) !== 1);
	const cost = (r: MatrixRow) => observedNumber(r.metrics?.cost_per_success) ?? Number.POSITIVE_INFINITY;
	const tied = top.length > 1;
	const sortedTop = tied ? [...top].sort((a, b) => cost(a) - cost(b) || a.rank - b.rank) : top;
	return [
		...sortedTop.map((row) => ({ row, rank: tied ? "=1" : String(row.rank), tied })),
		...rest.map((row) => ({ row, rank: String(row.rank), tied: false })),
	];
}

/** How many rows ran fewer than 2 tries per task, so "Solves every time" is not measured (M4-54). Pure. */
export function notMeasuredCount(rows: readonly Pick<MatrixRow, "trials">[]): number {
	return rows.filter((r) => r.trials < 2).length;
}

/**
 * One row per setup, the leader's ties first. Cells are the leaderboard's, so unknowns are hatched,
 * never 0. "Solves every time" sits under the solve rate so the cost fits (M4-49); config hashes are
 * left out and "What differs" is a button (M4-53); one note under the table says which setups have
 * no "Solves every time" (M4-54). Below sm one card per setup keeps the solve rate and the cost (M4-47).
 */
export function MatrixTable({ rows }: { rows: readonly MatrixRow[] }) {
	const { t } = useTranslation("bench");
	const { t: tc } = useTranslation("ufc3");
	const ranked = rankMatrixRows(rows);
	const unmeasured = notMeasuredCount(rows);
	const rankCell = (r: RankedMatrixRow) =>
		r.tied ? (
			<span title={tc("matrix.tiedRankHint")} data-testid={`matrix-rank-${r.row.arm}`}>
				{r.rank}
			</span>
		) : (
			<span data-testid={`matrix-rank-${r.row.arm}`}>{r.rank}</span>
		);
	return (
		<div className="flex flex-col gap-2">
			<ul className="flex flex-col gap-2 sm:hidden" data-testid="matrix-cards">
				{ranked.map((r) => (
					<li key={r.row.arm} className="bg-card flex flex-col gap-2 rounded-md border p-3" data-testid={`matrix-card-${r.row.arm}`}>
						<div className="flex items-start justify-between gap-2">
							<span className="text-foreground font-semibold tabular-nums" aria-label={tc("matrix.cardRank", { rank: r.rank })}>
								{rankCell(r)}
							</span>
							<span className="font-mono text-sm font-semibold">{r.row.arm}</span>
						</div>
						<AgentCell entry={r.row} showHash={false} />
						<div className="flex flex-wrap items-start justify-between gap-3">
							<SuccessCell entry={r.row} />
							<div className="flex flex-col items-end gap-0.5 text-sm">
								<span className="text-muted-foreground text-xs">{t("leaderboard.table.cost_per_success")}</span>
								<ValueCell value={r.row.metrics?.cost_per_success} kind="usd" />
							</div>
						</div>
					</li>
				))}
			</ul>
			<Table
				stickyColumns={4}
				data-testid="matrix-table"
				aria-label={t("matrix.table.label")}
				containerClassName="hidden rounded-sm sm:block"
			>
				<TableHeader>
					<TableRow className="hover:bg-transparent">
						<TableHead>{t("leaderboard.table.rank")}</TableHead>
						<TableHead>{t("leaderboard.table.tier")}</TableHead>
						<TableHead>{t("matrix.table.arm")}</TableHead>
						<TableHead>{t("leaderboard.table.agent")}</TableHead>
						<TableHead>{t("matrix.table.provider")}</TableHead>
						<TableHead title={`${t("leaderboard.table.successHint")} ${t("leaderboard.table.reliabilityHint")}`}>
							{t("leaderboard.table.success")}
						</TableHead>
						{MATRIX_METRIC_COLUMNS.map((column) => (
							<TableHead key={column.id} className="text-right" title={t(`leaderboard.table.${column.id}Hint`)}>
								{t(`leaderboard.table.${column.id}`)}
							</TableHead>
						))}
					</TableRow>
				</TableHeader>
				<TableBody>
					{ranked.map((r) => {
						const row = r.row;
						return (
							<TableRow key={row.arm} data-testid={`matrix-row-${row.arm}`} data-tier={tierOf(row)} data-rank={row.rank}>
								<TableCell className="text-foreground align-top font-semibold tabular-nums">{rankCell(r)}</TableCell>
								<TableCell className="align-top">
									<TierBadge entry={row} />
								</TableCell>
								<TableCell className="align-top font-mono font-semibold" data-testid={`matrix-arm-${row.arm}`}>
									{row.arm}
								</TableCell>
								<TableCell className="align-top">
									<AgentCell entry={row} showHash={false} />
								</TableCell>
								<TableCell className="text-muted-foreground align-top font-mono text-xs" data-testid={`matrix-provider-${row.arm}`}>
									{row.provider}
								</TableCell>
								<TableCell className="align-top">
									<div className="flex flex-col gap-1.5">
										<SuccessCell entry={row} />
										<div className="flex items-baseline gap-1.5 text-xs">
											<span className="text-muted-foreground">{t("leaderboard.table.reliability")}</span>
											<ReliabilityCell entry={row} inline />
										</div>
									</div>
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
			{unmeasured > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="matrix-not-measured-note">
					{tc("matrix.notMeasuredNote", { count: unmeasured })}
				</p>
			)}
		</div>
	);
}
