import { ReceiptFunnel } from "@/components/receipts/receiptFunnel";
import { catalogProbeFor } from "@/components/receipts/receipts.utils";
import { RunReceiptPanel } from "@/components/receipts/runReceiptPanel";
import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetCatalogQuery, useGetDiagnosticsQuery, useGetGridQuery } from "@/lib/store/apis/benchApi";
import type { DiagnosticsRow, GridCell, GridRow, ProbeSpec } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { Link } from "@tanstack/react-router";
import { OutageCard } from "@/components/safety/outageCard"; // Safety probes area (S1)
import { EntityLink } from "@/components/shell/entityLink";
import { parseAsString, useQueryState } from "nuqs";
import { probeLayerLabel } from "@/components/receipts/receipts.utils";
import { useTranslation } from "react-i18next";
import { SlowSkeleton } from "./slowSkeleton";

/**
 * The Receipts tab (DESIGN.md 18 screen 3): for each probe condition, how far the fault got per arm,
 * and each run's receipts. ?condition= and ?run= pick the condition and the run. The outage card
 * shows how each arm behaved while the model was down (safety probes area, S1).
 */
export function ReceiptsTab({ expId, suite }: { expId: string; suite?: string }) {
	const diagnostics = useGetDiagnosticsQuery(expId);
	return (
		<div className="flex flex-col gap-6" data-testid="receipts-screen">
			<ExperimentReceipts expId={expId} suite={suite} />
			{diagnostics.data && (
				<OutageCard meters={diagnostics.data.meters} arms={Object.keys(diagnostics.data.diagnostics[0]?.per_arm ?? {}).sort()} />
			)}
		</div>
	);
}

function ExperimentReceipts({ expId, suite }: { expId: string; suite?: string }) {
	const { t } = useTranslation("bench");
	const [conditionParam, setCondition] = useQueryState("condition", parseAsString);
	const [runParam, setRun] = useQueryState("run", parseAsString);
	const diagnostics = useGetDiagnosticsQuery(expId);
	const catalog = useGetCatalogQuery();

	if (diagnostics.isLoading)
		return <SlowSkeleton rows={8} testId="receipts-diagnostics-loading" onRetry={() => void diagnostics.refetch()} />;
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
	const probe = catalogProbeFor(catalog.data, suite, row.condition);

	return (
		<div className="flex flex-col gap-6">
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

			<ConditionRuns expId={expId} suite={suite} condition={row.condition} selectedRun={runParam} onSelectRun={(id) => void setRun(id)} />
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
						{layer ? t("receipts.layer", { layer: probeLayerLabel(layer, t) }) : t("receipts.layerUnknown")}
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
	suite,
	condition,
	selectedRun,
	onSelectRun,
}: {
	expId: string;
	suite?: string;
	condition: string;
	selectedRun: string | null;
	onSelectRun: (runId: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const grid = useGetGridQuery(expId);
	const rows = grid.data?.rows.filter((r) => r.condition === condition) ?? [];
	const arms = grid.data?.arms ?? [];

	return (
		<Section title={t("receipts.runs.title")} testId="receipts-runs-section">
			{grid.isLoading ? (
				<SlowSkeleton rows={4} testId="receipts-runs-loading" onRetry={() => void grid.refetch()} />
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
						<RunsTable expId={expId} suite={suite} rows={rows} arms={arms} selectedRun={selectedRun} onSelectRun={onSelectRun} />
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
	expId,
	suite,
	rows,
	arms,
	selectedRun,
	onSelectRun,
}: {
	expId: string;
	suite?: string;
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
						<TableCell className="text-xs">
							<EntityLink kind="task" id={tr.task} suite={suite} />
						</TableCell>
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
								to="/compare"
								search={compareSearch(tr.pairId, expId) as never}
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
			{cell?.outcome && <OutcomeBadge outcome={cell.outcome} testId={`receipts-run-${runId}-outcome`} className="px-1 py-0 text-[11px]" />}
		</button>
	);
}