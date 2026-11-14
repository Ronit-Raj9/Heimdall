import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { LeaderboardEntry } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { METRIC_COLUMNS, RESILIENCE_PREFIX, tierGroupStarts, tierOf, type LeaderboardSort, type SortKey } from "./leaderboard.utils";
import { AgentCell, ExperimentLinks, ReliabilityCell, ResilienceCell, SuccessCell, TierBadge } from "./leaderboardCells";

export interface LeaderboardTableProps {
	/** Entries in the order to show them. */
	rows: readonly LeaderboardEntry[];
	/** Fault conditions with a resilience column, in order. Empty off the clean board. */
	faults: readonly string[];
	sort: LeaderboardSort;
	onSort: (key: SortKey) => void;
	/** Significance level of the tiers, for the tier group labels. */
	alpha: number;
}

/**
 * One row per agent configuration. Rank and tier come from the server; a column click sorts on the
 * client and the rank keeps its meaning. In rank order, tier groups are marked with a divider row.
 */
export function LeaderboardTable({ rows, faults, sort, onSort, alpha }: LeaderboardTableProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const groupStarts = sort.key === "rank" ? tierGroupStarts(rows) : new Map<number, 1 | 2>();
	const columnCount = 5 + METRIC_COLUMNS.length + faults.length + 2;
	const head = (key: SortKey, label: string, hint?: string, align: "left" | "right" = "right") => (
		<SortableHead key={key} sortKey={key} label={label} hint={hint} sort={sort} onSort={onSort} align={align} />
	);

	return (
		<Table data-testid="leaderboard-table" aria-label={t("leaderboard.table.label")} containerClassName="rounded-sm">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					{head("rank", t("leaderboard.table.rank"), undefined, "left")}
					<TableHead>{t("leaderboard.table.tier")}</TableHead>
					{head("agent", t("leaderboard.table.agent"), undefined, "left")}
					{head("success", t("leaderboard.table.success"), t("leaderboard.table.successHint"), "left")}
					{head("reliability", t("leaderboard.table.reliability"), t("leaderboard.table.reliabilityHint"))}
					{METRIC_COLUMNS.map((column) => head(column.id, t(`leaderboard.table.${column.id}`), t(`leaderboard.table.${column.id}Hint`)))}
					{faults.map((fault) =>
						head(
							`${RESILIENCE_PREFIX}${fault}`,
							`${fault} ${t("leaderboard.table.resilienceVs")}`,
							t("leaderboard.table.resilienceHint", { condition: fault }),
						),
					)}
					{head("tasks", t("leaderboard.table.tasks"), t("leaderboard.table.tasksHint"))}
					<TableHead>{t("leaderboard.table.experiments")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((entry, index) => {
					const tier = tierOf(entry);
					const groupTier = groupStarts.get(index);
					return (
						<Fragment key={entry.config_hash}>
							{groupTier !== undefined && (
								<TableRow className="hover:bg-transparent" data-testid={`leaderboard-tier-group-${index}`} data-tier={groupTier}>
									<TableCell
										colSpan={columnCount}
										className={cn(
											"bg-muted/40 text-muted-foreground py-1 text-[11px] font-medium tracking-wide uppercase",
											index > 0 && "border-foreground/20 border-t-2",
										)}
									>
										{groupTier === 1 ? t("leaderboard.tier.groupOne") : t("leaderboard.tier.groupTwo", { alpha })}
									</TableCell>
								</TableRow>
							)}
							<TableRow data-testid={`leaderboard-row-${entry.rank}`} data-tier={tier} data-config-hash={entry.config_hash}>
								<TableCell
									className={cn(
										"text-foreground align-top font-semibold tabular-nums",
										// A thin band on the row edge keeps the tier visible when sorted by another column.
										tier === 1 ? "shadow-[inset_3px_0_0_var(--chart-success)]" : "shadow-[inset_3px_0_0_var(--chart-neutral)]",
									)}
									data-testid={`leaderboard-rank-${entry.rank}`}
								>
									{entry.rank}
								</TableCell>
								<TableCell className="align-top">
									<TierBadge entry={entry} />
								</TableCell>
								<TableCell className="align-top">
									<AgentCell entry={entry} />
								</TableCell>
								<TableCell className="align-top">
									<SuccessCell entry={entry} />
								</TableCell>
								<TableCell className="text-right align-top">
									<ReliabilityCell entry={entry} />
								</TableCell>
								{METRIC_COLUMNS.map((column) => (
									<TableCell key={column.id} className="text-right align-top" data-testid={`leaderboard-metric-${entry.rank}-${column.id}`}>
										<ValueCell value={entry.metrics?.[column.id]} kind={column.kind} />
									</TableCell>
								))}
								{faults.map((fault) => (
									<TableCell key={fault} className="text-right align-top">
										<ResilienceCell
											value={entry.resilience?.[fault]}
											condition={fault}
											testId={`leaderboard-resilience-${entry.rank}-${fault}`}
										/>
									</TableCell>
								))}
								<TableCell className="text-right align-top tabular-nums" data-testid={`leaderboard-tasks-${entry.rank}`}>
									{t("leaderboard.cell.tasksRuns", { tasks: formatCount(entry.tasks, locale), runs: formatCount(entry.runs, locale) })}
								</TableCell>
								<TableCell className="align-top">
									<ExperimentLinks entry={entry} />
								</TableCell>
							</TableRow>
						</Fragment>
					);
				})}
			</TableBody>
		</Table>
	);
}

interface SortableHeadProps {
	sortKey: SortKey;
	label: string;
	hint?: string;
	sort: LeaderboardSort;
	onSort: (key: SortKey) => void;
	align: "left" | "right";
}

/** A header that sorts its column: a real button, so it is reachable and operable by keyboard. */
function SortableHead({ sortKey, label, hint, sort, onSort, align }: SortableHeadProps) {
	const { t } = useTranslation("bench");
	const active = sort.key === sortKey;
	const Icon = !active ? ArrowUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
	return (
		<TableHead
			aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
			className={cn(align === "right" && "text-right")}
			data-testid={`leaderboard-head-${sortKey}`}
		>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				title={hint}
				aria-label={
					active
						? `${t("leaderboard.table.sortBy", { column: label })}, ${t(sort.dir === "asc" ? "leaderboard.table.sortedAsc" : "leaderboard.table.sortedDesc")}`
						: t("leaderboard.table.sortBy", { column: label })
				}
				className={cn(
					"focus-visible:ring-ring/50 -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
					active ? "text-foreground" : "text-muted-foreground hover:text-foreground",
					align === "right" && "flex-row-reverse",
				)}
				data-testid={`leaderboard-sort-${sortKey}`}
			>
				<span>{label}</span>
				<Icon className={cn("size-3.5 shrink-0", !active && "opacity-50")} aria-hidden="true" />
			</button>
		</TableHead>
	);
}