import { EntityLink } from "@/components/shell/entityLink";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import { pairOpenable } from "@/lib/store/apis/benchApi";
import type { Grid, GridCell, GridRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Check, CircleHelp, ShieldAlert, X } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { cellTone, formatSignedPp, sortGridRows, type CellTone } from "./verdict.utils";

export const CELL_CLASS = {
	pass: "bg-chart-success text-chart-success-on border-transparent",
	zero: "bg-chart-error/85 text-white border-transparent",
	integrity: "bg-chart-error text-white border-chart-error-ink ring-2 ring-chart-error-ink/60",
	unknown: "bench-hatched text-muted-foreground border-dashed border-muted-foreground/50",
} as const satisfies Record<CellTone, string>;

const CELL_ICON = {
	pass: Check,
	zero: X,
	integrity: ShieldAlert,
	unknown: CircleHelp,
} as const satisfies Record<CellTone, typeof Check>;

const LEGEND: readonly CellTone[] = ["pass", "zero", "integrity", "unknown"];

export interface TaskGridProps {
	grid: Grid;
	/** Opens the run comparison for one pair (every arm's run of one task, condition and trial). */
	onOpenPair: (pairId: string) => void;
	/** The experiment's suite; with it each task id links to the task in its suite. */
	suite?: string;
	className?: string;
}

/** Task x condition rows, discordant first; one cell per arm and trial, coloured by outcome. */
export function TaskGrid({ grid, onOpenPair, suite, className }: TaskGridProps) {
	const { t } = useTranslation("bench");
	const rows = useMemo(() => sortGridRows(grid.rows), [grid.rows]);
	const trials = useMemo(() => Array.from({ length: Math.max(grid.trials, 0) }, (_, i) => i + 1), [grid.trials]);

	if (rows.length === 0) {
		return (
			<p className={cn("text-muted-foreground text-sm", className)} data-testid="verdict-grid-empty">
				{t("verdict.grid.empty")}
			</p>
		);
	}

	return (
		<div className={cn("flex flex-col gap-3", className)}>
			<Table stickyColumns={1} data-testid="verdict-grid-table">
				<TableHeader>
					<TableRow>
						<TableHead rowSpan={2}>{t("verdict.grid.task")}</TableHead>
						<TableHead rowSpan={2}>{t("verdict.grid.condition")}</TableHead>
						{grid.arms.map((arm) => (
							<TableHead key={arm} colSpan={trials.length} className="h-7 text-center">
								{t("verdict.grid.armTrials", { arm })}
							</TableHead>
						))}
						<TableHead rowSpan={2} className="text-right" title={t("verdict.grid.dHint")}>
							{t("verdict.grid.d")}
						</TableHead>
					</TableRow>
					<TableRow>
						{grid.arms.flatMap((arm) =>
							trials.map((trial) => (
								<TableHead key={`${arm}-${trial}`} className="text-muted-foreground h-7 px-1 text-center font-mono text-xs">
									{t("verdict.grid.trial", { arm, trial })}
								</TableHead>
							)),
						)}
					</TableRow>
				</TableHeader>
				<TableBody>
					{rows.map((row) => (
						<GridRowView
							key={`${row.task}:${row.condition}`}
							row={row}
							arms={grid.arms}
							trials={trials}
							onOpenPair={onOpenPair}
							suite={suite}
						/>
					))}
				</TableBody>
			</Table>
			<ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs" data-testid="verdict-grid-legend">
				{LEGEND.map((tone) => {
					const Icon = CELL_ICON[tone];
					return (
						<li key={tone} className="flex items-center gap-1.5">
							<span className={cn("inline-flex size-4 items-center justify-center rounded-sm border", CELL_CLASS[tone])}>
								<Icon className="size-3" aria-hidden="true" />
							</span>
							{t(`verdict.grid.legend.${tone}`)}
						</li>
					);
				})}
			</ul>
		</div>
	);
}

function GridRowView({
	row,
	arms,
	trials,
	onOpenPair,
	suite,
}: {
	row: GridRow;
	arms: string[];
	trials: number[];
	onOpenPair: (pairId: string) => void;
	suite?: string;
}) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const byArmTrial = useMemo(() => new Map(row.cells.map((cell) => [`${cell.arm}:${cell.trial}`, cell])), [row.cells]);

	return (
		<TableRow data-testid={`verdict-grid-row-${row.task}-${row.condition}`} data-discordant={row.discordant}>
			<TableCell className="font-mono text-xs">
				<span className="inline-flex items-center gap-2">
					{suite ? <EntityLink kind="task" id={row.task} suite={suite} /> : row.task}
					{row.discordant && (
						<span className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1 text-xs uppercase">
							{t("verdict.grid.discordant")}
						</span>
					)}
				</span>
			</TableCell>
			<TableCell className="font-mono text-xs">{row.condition}</TableCell>
			{arms.flatMap((arm) =>
				trials.map((trial) => (
					<TableCell key={`${arm}-${trial}`} className="px-1 text-center">
						<OutcomeCell cell={byArmTrial.get(`${arm}:${trial}`)} row={row} arm={arm} trial={trial} onOpenPair={onOpenPair} />
					</TableCell>
				)),
			)}
			<TableCell className={cn("text-right text-xs tabular-nums", row.diff.d === 0 && "text-muted-foreground")}>
				{t("verdict.card.pp", { value: formatSignedPp(row.diff.d, locale) })}
			</TableCell>
		</TableRow>
	);
}

function OutcomeCell({
	cell,
	row,
	arm,
	trial,
	onOpenPair,
}: {
	cell: GridCell | undefined;
	row: GridRow;
	arm: string;
	trial: number;
	onOpenPair: (pairId: string) => void;
}) {
	const { t } = useTranslation("bench");
	const tone = cell ? cellTone(cell.outcome, cell.y) : "unknown";
	const Icon = CELL_ICON[tone];
	const outcome = cell?.outcome ? t(`verdict.grid.outcome.${cell.outcome}`) : t("verdict.grid.outcome.notRun");
	const label = t("verdict.grid.cellLabel", { task: row.task, condition: row.condition, arm, trial, outcome });

	if (!cell) {
		return (
			<span
				className={cn("inline-flex size-7 items-center justify-center rounded-sm border", CELL_CLASS.unknown)}
				aria-label={label}
				title={label}
				data-testid={`verdict-grid-cell-missing-${row.task}-${row.condition}-${arm}-${trial}`}
			>
				<Icon className="size-3.5" aria-hidden="true" />
			</span>
		);
	}

	// Fixture mode: a pair without a fixture shows its outcome but does not open (QC2-29-04).
	if (!pairOpenable(cell.pair_id)) {
		return (
			<span
				aria-label={label}
				title={label}
				data-testid={`verdict-grid-cell-${cell.slot_id}`}
				data-pair-id={cell.pair_id}
				data-outcome={cell.outcome ?? "none"}
				data-tone={tone}
				data-openable="false"
				className={cn("inline-flex size-7 items-center justify-center rounded-sm border", CELL_CLASS[tone])}
			>
				<Icon className="size-3.5" aria-hidden="true" />
			</span>
		);
	}

	return (
		<button
			type="button"
			onClick={() => onOpenPair(cell.pair_id)}
			aria-label={label}
			title={label}
			data-testid={`verdict-grid-cell-${cell.slot_id}`}
			data-pair-id={cell.pair_id}
			data-outcome={cell.outcome ?? "none"}
			data-tone={tone}
			className={cn(
				"focus-visible:ring-ring inline-flex size-7 cursor-pointer items-center justify-center rounded-sm border transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-offset-1 focus-visible:outline-none",
				CELL_CLASS[tone],
			)}
		>
			<Icon className="size-3.5" aria-hidden="true" />
		</button>
	);
}