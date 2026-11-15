import { useExperimentId } from "@/components/experiments/useExperimentId";
import { ModeBadge } from "@/components/modeBadge";
import { ReceiptFunnel } from "@/components/receipts/receiptFunnel";
import { RunReceiptPanel } from "@/components/receipts/runReceiptPanel";
import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetCatalogQuery, useGetDiagnosticsQuery, useGetGridQuery } from "@/lib/store/apis/benchApi";
import type { DiagnosticsRow, GridCell, GridRow, ProbeSpec } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/** Receipt panel (DESIGN.md 18 screen 3): /receipts?exp=&condition=&run=. */
export default function ReceiptsView() {
	const { t } = useTranslation("bench");
	const [expParam] = useQueryState("exp", parseAsString);
	const experiment = useExperimentId(expParam);

	return (
		<div className="flex flex-col gap-6" data-testid="receipts-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("receipts.title")}</h1>
				<p className="text-muted-foreground text-sm">{t("receipts.subtitle")}</p>
			</header>
			{experiment.isLoading ? (
				<BlockSkeleton rows={8} testId="receipts-loading" />
			) : experiment.error ? (
				<LoadError title={t("receipts.listError")} error={experiment.error} onRetry={experiment.refetch} testId="receipts-list-error" />
			) : !experiment.expId ? (
				<EmptyNote testId="receipts-no-experiments">{t("receipts.noExperiments")}</EmptyNote>
			) : (
				<ExperimentReceipts expId={experiment.expId} />
			)}
		</div>
	);
}

function ExperimentReceipts({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const [conditionParam, setCondition] = useQueryState("condition", parseAsString);
	const [runParam, setRun] = useQueryState("run", parseAsString);
	const diagnostics = useGetDiagnosticsQuery(expId);
	const catalog = useGetCatalogQuery();

	if (diagnostics.isLoading) return <BlockSkeleton rows={8} testId="receipts-diagnostics-loading" />;
	if (diagnostics.error || !diagnostics.data) {
		return (
			<LoadError
				title={t("receipts.diagnosticsError")}
				error={diagnostics.error}
				onRetry={() => void diagnostics.refetch()}
				testId="receipts-diagnostics-error"
			/>
		);
	}

	const probeRows = diagnostics.data.diagnostics.filter((row) => row.condition !== "clean");
	if (probeRows.length === 0) return <EmptyNote testId="receipts-empty">{t("receipts.empty")}</EmptyNote>;
	const row = probeRows.find((r) => r.condition === conditionParam) ?? probeRows[0];
	const probe = catalog.data?.probes.find((p) => p.id === row.condition);

	return (
		<div className="flex flex-col gap-6">
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-muted-foreground font-mono text-xs">{t("receipts.experiment", { id: expId })}</span>
				<ModeBadge mode={diagnostics.data.mode} />
			</div>

			<div className="flex flex-col gap-2">
				<span className="text-muted-foreground text-xs font-medium" id="receipts-conditions-label">
					{t("receipts.conditions")}
				</span>
				<div className="flex flex-wrap gap-1.5" role="group" aria-labelledby="receipts-conditions-label">
					{probeRows.map((r) => {
						const active = r.condition === row.condition;
						return (
							<button
								key={r.condition}
								type="button"
								aria-pressed={active}
								onClick={() => {
									void setCondition(r.condition);
									void setRun(null);
								}}
								data-testid={`receipts-condition-${r.condition}`}
								className={cn(
									"focus-visible:ring-ring/50 cursor-pointer rounded-sm border px-2.5 py-1 font-mono text-xs transition-colors focus-visible:ring-[3px]",
									active ? "bg-primary text-primary-foreground border-primary" : "hover:bg-accent",
								)}
							>
								{r.condition}
							</button>
						);
					})}
				</div>
			</div>

			<ConditionFunnel row={row} probe={probe} />

			<ConditionRuns expId={expId} condition={row.condition} selectedRun={runParam} onSelectRun={(id) => void setRun(id)} />
		</div>
	);
}

function ConditionFunnel({ row, probe }: { row: DiagnosticsRow; probe: ProbeSpec | undefined }) {
	const { t } = useTranslation("bench");
	const ratios = row.receipts ?? {};
	const arms = Object.keys(row.per_arm).sort();
	const layer = probe?.layer ?? null;

	return (
		<Section
			title={t("receipts.funnel.title")}
			description={t("receipts.funnel.description")}
			testId="receipts-funnel-section"
			actions={
				<span className="flex flex-wrap items-center gap-2 text-xs">
					<span className="text-muted-foreground font-mono">{row.axis}</span>
					<span className="rounded-sm border px-1.5 py-0.5 font-medium" data-testid="receipts-layer">
						{layer ? t("receipts.layer", { layer: t(`receipts.layers.${layer}`) }) : t("receipts.layerUnknown")}
					</span>
				</span>
			}
		>
			{Object.keys(ratios).length === 0 ? (
				<EmptyNote testId="receipts-funnel-empty">{t("receipts.funnel.noReceipts")}</EmptyNote>
			) : (
				<Card className="py-0 shadow-none">
					<ReceiptFunnel layer={layer} arms={arms} ratios={ratios} />
				</Card>
			)}
		</Section>
	);
}

function ConditionRuns({
	expId,
	condition,
	selectedRun,
	onSelectRun,
}: {
	expId: string;
	condition: string;
	selectedRun: string | null;
	onSelectRun: (runId: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const grid = useGetGridQuery(expId);
	const rows = grid.data?.rows.filter((r) => r.condition === condition) ?? [];
	const arms = grid.data?.arms ?? [];

	return (
		<Section title={t("receipts.runs.title")} description={t("receipts.runs.description")} testId="receipts-runs-section">
			{grid.isLoading ? (
				<BlockSkeleton rows={4} testId="receipts-runs-loading" />
			) : grid.error ? (
				<LoadError
					title={t("receipts.runs.gridError")}
					error={grid.error}
					onRetry={() => void grid.refetch()}
					testId="receipts-runs-error"
				/>
			) : rows.length === 0 ? (
				<EmptyNote testId="receipts-runs-empty">{t("receipts.runs.empty")}</EmptyNote>
			) : (
				<div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
					<Card className="py-0 shadow-none">
						<RunsTable rows={rows} arms={arms} selectedRun={selectedRun} onSelectRun={onSelectRun} />
					</Card>
					{selectedRun && (
						<div className="xl:sticky xl:top-0">
							<RunReceiptPanel runId={selectedRun} probeId={condition} onClose={() => onSelectRun(null)} />
						</div>
					)}
				</div>
			)}
		</Section>
	);
}

interface TrialRow {
	task: string;
	trial: number;
	pairId: string;
	cells: Record<string, GridCell>;
}

function trialRows(rows: readonly GridRow[]): TrialRow[] {
	const out: TrialRow[] = [];
	for (const row of rows) {
		const byTrial = new Map<number, TrialRow>();
		for (const cell of row.cells) {
			const entry = byTrial.get(cell.trial) ?? { task: row.task, trial: cell.trial, pairId: cell.pair_id, cells: {} };
			entry.cells[cell.arm] = cell;
			byTrial.set(cell.trial, entry);
		}
		out.push(...byTrial.values());
	}
	return out.sort((a, b) => a.task.localeCompare(b.task) || a.trial - b.trial);
}

function RunsTable({
	rows,
	arms,
	selectedRun,
	onSelectRun,
}: {
	rows: readonly GridRow[];
	arms: readonly string[];
	selectedRun: string | null;
	onSelectRun: (runId: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	return (
		<Table stickyColumns={1} data-testid="receipts-runs-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("receipts.runs.task")}</TableHead>
					<TableHead>{t("receipts.runs.trial")}</TableHead>
					<TableHead>{t("receipts.runs.runsByArm")}</TableHead>
					<TableHead />
				</TableRow>
			</TableHeader>
			<TableBody>
				{trialRows(rows).map((tr) => (
					<TableRow key={`${tr.task}-${tr.trial}`} data-testid={`receipts-run-row-${tr.task}-${tr.trial}`}>
						<TableCell className="font-mono text-xs">{tr.task}</TableCell>
						<TableCell className="tabular-nums">{tr.trial}</TableCell>
						<TableCell>
							<div className="flex flex-wrap gap-1.5">
								{arms.map((arm) => (
									<RunButton key={arm} arm={arm} cell={tr.cells[arm]} selected={selectedRun} onSelect={onSelectRun} />
								))}
							</div>
						</TableCell>
						<TableCell className="text-right">
							<Link
								to="/runs"
								search={{ pair: tr.pairId }}
								data-testid={`receipts-compare-${tr.pairId}`}
								className="text-primary text-xs underline-offset-4 hover:underline"
							>
								{t("receipts.runs.comparePair")}
							</Link>
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function RunButton({
	arm,
	cell,
	selected,
	onSelect,
}: {
	arm: string;
	cell: GridCell | undefined;
	selected: string | null;
	onSelect: (runId: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const runId = cell?.run_id ?? null;
	if (!runId) {
		return (
			<span className="text-muted-foreground rounded-sm border border-dashed px-2 py-1 text-xs">
				{arm} {t("receipts.runs.notRun")}
			</span>
		);
	}
	const active = selected === runId;
	return (
		<button
			type="button"
			aria-pressed={active}
			aria-label={t("receipts.runs.show", { run: runId })}
			onClick={() => onSelect(active ? null : runId)}
			data-testid={`receipts-run-${runId}`}
			className={cn(
				"focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 text-xs transition-colors focus-visible:ring-[3px]",
				active ? "border-primary bg-primary/10" : "hover:bg-accent",
			)}
		>
			<span className="font-semibold">{arm}</span>
			{cell?.outcome && <OutcomeBadge outcome={cell.outcome} testId={`receipts-run-${runId}-outcome`} className="px-1 py-0 text-[10px]" />}
		</button>
	);
}