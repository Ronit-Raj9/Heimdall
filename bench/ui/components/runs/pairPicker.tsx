import { useExperimentId } from "@/components/experiments/useExperimentId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { EntityLink } from "@/components/shell/entityLink";
import { pairOpenable, useGetExperimentQuery, useGetGridQuery } from "@/lib/store/apis/benchApi";
import { frozenSuiteRef, type GridRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Check, ChevronRight, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import "./uf5bI18n";
import { outcomeTone, pairsOfRow, type PairCell } from "./runs.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "./screenStates";

/** One setup of an experiment as GET /experiments/{id} names it. */
export interface SetupName {
	arm: string;
	agent: string;
	model: string;
}

/** "A: claude-code gpt-5" per arm that has a known setup, in the grid's arm order. */
export function setupNames(arms: readonly string[], setups: readonly SetupName[] | undefined): string[] {
	if (!setups?.length) return [];
	return arms.flatMap((arm) => {
		const s = setups.find((x) => x.arm === arm);
		const what = s ? [s.agent, s.model].filter(Boolean).join(" ") : "";
		return what ? [`${arm}: ${what}`] : [];
	});
}

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
	// The experiment's suite, so each task links to its verifier and capabilities.
	const status = useGetExperimentQuery(experiment.expId ?? skipToken);

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
				<PairTable
					rows={grid.data.rows}
					arms={grid.data.arms}
					setups={status.data?.setups}
					suite={frozenSuiteRef(status.data?.suite, status.data?.suite_version)}
					onPick={onPick}
				/>
			)}
		</Section>
	);
}

export function PairTable({
	rows,
	arms,
	suite,
	setups,
	onPick,
}: {
	rows: readonly GridRow[];
	arms: readonly string[];
	/** The experiment's setups (optional on the answer): named above the table so A and B are known (X-33). */
	setups?: readonly SetupName[];
	suite?: string;
	onPick: (pairId: string) => void;
}) {
	const { t } = useTranslation("bench");
	const { t: t5 } = useTranslation("uf5b");
	// The condition column only when some row ran under a fault; "clean" reads "No fault" (X-33).
	const showCondition = rows.some((r) => r.condition !== "clean");
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<p className="text-muted-foreground text-xs" data-testid="pair-picker-legend">
				{t5("pair.legend")}
			</p>
			{setupNames(arms, setups).length > 0 && (
				<p className="text-sm" data-testid="pair-picker-setups">
					{setupNames(arms, setups).join(" / ")}
				</p>
			)}
			<Table stickyColumns={1} data-testid="pair-picker-table">
				<TableHeader>
					<TableRow>
						<TableHead className="text-sm">{t("runs.picker.task")}</TableHead>
						{showCondition && <TableHead className="text-sm">{t("runs.picker.condition")}</TableHead>}
						<TableHead className="text-sm">{t("runs.picker.trials")}</TableHead>
						<TableHead className="w-6" aria-hidden="true" />
					</TableRow>
				</TableHeader>
				<TableBody>
					{rows.map((row) => {
						const first = pairsOfRow(row).find((p) => p.hasRuns && pairOpenable(p.pairId));
						return (
							<TableRow
								key={`${row.task}-${row.condition}`}
								data-testid={`pair-picker-row-${row.task}-${row.condition}`}
								// The whole row opens its first pair; the task link and the trial buttons keep their own action.
								onClick={(e) => {
									if (!first || (e.target as HTMLElement).closest("a,button")) return;
									onPick(first.pairId);
								}}
								className={cn(first && "hover:bg-muted cursor-pointer")}
							>
								<TableCell className="text-sm">
									<EntityLink kind="task" id={row.task} suite={suite} testId={`pair-picker-task-${row.task}`} />
								</TableCell>
								{showCondition && (
									<TableCell>
										<span className="flex items-center gap-2">
											<span className="text-sm">{row.condition === "clean" ? t5("pair.noFault") : row.condition}</span>
											{row.discordant && (
												<span className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1.5 text-xs font-medium">
													{t("runs.picker.discordant")}
												</span>
											)}
										</span>
									</TableCell>
								)}
								<TableCell>
									<div className="flex flex-wrap items-center gap-1.5">
										{pairsOfRow(row).map((pair) => (
											<PairButton key={pair.pairId} pair={pair} arms={arms} row={row} onPick={onPick} />
										))}
										{!showCondition && row.discordant && (
											<span className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1.5 text-xs font-medium">
												{t("runs.picker.discordant")}
											</span>
										)}
									</div>
								</TableCell>
								<TableCell className="w-6 px-1">
									{first && <ChevronRight className="text-muted-foreground size-4" aria-hidden="true" />}
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
		</div>
	);
}

// Each dot carries a check or a cross, so the result does not rest on colour alone (M5-27).
const DOT_CLASS = {
	pass: "bg-chart-success text-white",
	zero: "bg-chart-error text-white",
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
	const texts = arms.map((arm) => {
		const outcome = pair.outcomes[arm] ?? null;
		return t("runs.picker.cellOutcome", { arm, outcome: outcome ? t(`runs.outcome.${outcome}`) : t("runs.picker.notRun") });
	});
	return (
		<button
			type="button"
			onClick={() => onPick(pair.pairId)}
			disabled={!pair.hasRuns || !pairOpenable(pair.pairId)}
			title={label}
			aria-label={`${label}. ${texts.join(", ")}`}
			data-testid={`pair-picker-open-${pair.pairId}`}
			className="hover:bg-accent focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 text-xs transition-colors focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
		>
			<span>{t("runs.picker.trial", { trial: pair.trial })}</span>
			{arms.map((arm) => {
				const outcome = pair.outcomes[arm] ?? null;
				const text = t("runs.picker.cellOutcome", { arm, outcome: outcome ? t(`runs.outcome.${outcome}`) : t("runs.picker.notRun") });
				return (
					<span key={arm} className="inline-flex" title={text}>
						<span
							className={cn("inline-flex size-3.5 items-center justify-center rounded-full", DOT_CLASS[outcomeTone(outcome)])}
							aria-hidden="true"
						>
							{outcomeTone(outcome) === "pass" ? (
								<Check className="size-2.5" strokeWidth={3} />
							) : outcomeTone(outcome) === "zero" ? (
								<X className="size-2.5" strokeWidth={3} />
							) : null}
						</span>
					</span>
				);
			})}
		</button>
	);
}