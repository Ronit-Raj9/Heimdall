import { useExperimentId } from "@/components/experiments/useExperimentId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetGridQuery } from "@/lib/store/apis/benchApi";
import type { GridRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useTranslation } from "react-i18next";
import { outcomeTone, pairsOfRow, type PairCell } from "./runs.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "./screenStates";

export interface PairPickerProps {
	/** The experiment named in the URL; the newest experiment when null. */
	expParam: string | null;
	onPick: (pairId: string) => void;
}

/** Lists the pairs of one experiment's task grid, discordant rows first, so a pair can be opened. */
export function PairPicker({ expParam, onPick }: PairPickerProps) {
	const { t } = useTranslation("bench");
	const experiment = useExperimentId(expParam);
	const grid = useGetGridQuery(experiment.expId ?? skipToken);

	if (experiment.isLoading) return <BlockSkeleton rows={5} testId="pair-picker-loading" />;
	if (experiment.error) {
		return (
			<LoadError title={t("runs.picker.listError")} error={experiment.error} onRetry={experiment.refetch} testId="pair-picker-list-error" />
		);
	}
	if (experiment.isEmpty || !experiment.expId)
		return <EmptyNote testId="pair-picker-no-experiments">{t("runs.picker.noExperiments")}</EmptyNote>;

	return (
		<Section title={t("runs.picker.title")} description={t("runs.picker.description", { exp: experiment.expId })} testId="pair-picker">
			{grid.isLoading ? (
				<BlockSkeleton rows={5} testId="pair-picker-grid-loading" />
			) : grid.error ? (
				<LoadError
					title={t("runs.picker.gridError")}
					error={grid.error}
					onRetry={() => void grid.refetch()}
					testId="pair-picker-grid-error"
				/>
			) : !grid.data || grid.data.rows.length === 0 ? (
				<EmptyNote testId="pair-picker-empty">{t("runs.picker.noRows")}</EmptyNote>
			) : (
				<PairTable rows={grid.data.rows} arms={grid.data.arms} onPick={onPick} />
			)}
		</Section>
	);
}

function PairTable({ rows, arms, onPick }: { rows: readonly GridRow[]; arms: readonly string[]; onPick: (pairId: string) => void }) {
	const { t } = useTranslation("bench");
	return (
		<Table stickyColumns={1} data-testid="pair-picker-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("runs.picker.task")}</TableHead>
					<TableHead>{t("runs.picker.condition")}</TableHead>
					<TableHead>{t("runs.picker.trials")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={`${row.task}-${row.condition}`} data-testid={`pair-picker-row-${row.task}-${row.condition}`}>
						<TableCell className="font-mono text-xs">{row.task}</TableCell>
						<TableCell>
							<span className="flex items-center gap-2">
								<span className="font-mono text-xs">{row.condition}</span>
								{row.discordant && (
									<span className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1 text-[10px] font-medium">
										{t("runs.picker.discordant")}
									</span>
								)}
							</span>
						</TableCell>
						<TableCell>
							<div className="flex flex-wrap gap-1.5">
								{pairsOfRow(row).map((pair) => (
									<PairButton key={pair.pairId} pair={pair} arms={arms} row={row} onPick={onPick} />
								))}
							</div>
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

const DOT_CLASS = {
	pass: "bg-chart-success",
	zero: "bg-chart-error",
	unknown: "bench-hatched border border-dashed border-muted-foreground/50",
} as const;

function PairButton({
	pair,
	arms,
	row,
	onPick,
}: {
	pair: PairCell;
	arms: readonly string[];
	row: GridRow;
	onPick: (pairId: string) => void;
}) {
	const { t } = useTranslation("bench");
	const label = t("runs.picker.openHint", { trial: pair.trial, task: row.task, condition: row.condition });
	return (
		<button
			type="button"
			onClick={() => onPick(pair.pairId)}
			disabled={!pair.hasRuns}
			title={label}
			data-testid={`pair-picker-open-${pair.pairId}`}
			className="hover:bg-accent focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 text-xs transition-colors focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
		>
			<span>{t("runs.picker.trial", { trial: pair.trial })}</span>
			{arms.map((arm) => {
				const outcome = pair.outcomes[arm] ?? null;
				const text = t("runs.picker.cellOutcome", { arm, outcome: outcome ? t(`runs.outcome.${outcome}`) : t("runs.picker.notRun") });
				return (
					<span key={arm} className="inline-flex" title={text}>
						<span className={cn("inline-block size-2.5 rounded-full", DOT_CLASS[outcomeTone(outcome)])} aria-hidden="true" />
						<span className="sr-only">{text}</span>
					</span>
				);
			})}
		</button>
	);
}