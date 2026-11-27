import "@/app/evaluations_/$evalId/views/fw08I18n";
import { PageHeader } from "@/components/shell/pageHeader";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { CalendarClock, Search } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useConfirm } from "@/components/platform/confirmDialog";
import { useDeleteEvaluationMutation } from "@/lib/store/apis/dataOpsApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	EVALUATION_POLL_MS,
	EVALUATION_SORTS,
	EVALUATION_STATUS_FILTERS,
	FAILURE_TEST_MODE,
	evaluationAgents,
	evaluationBenchmarks,
	evaluationStatusWord,
	evaluationKind,
	evaluationResultText,
	evaluationSpend,
	evaluationFiltersSearch,
	filterEvaluations,
	formatCreated,
	isActiveStatus,
	modeKey,
	parseEvaluationFilters,
	type EvaluationFilters,
} from "./evaluation.utils";
import { EvaluationLivePill } from "./evaluationLivePill";
import { EvaluationKindLabel, EvaluationStatusPill } from "./evaluationWords";
import { usd } from "@/components/reportcard/reportCard.utils";

/** /evaluations: every evaluation with search, filters and sort kept in the URL; each row opens its progress and report card. */
export default function EvaluationListView() {
	const { t } = useTranslation("evaluations");
	const list = useListEvaluationsQuery();
	const search = useSearch({ strict: false }) as Record<string, unknown>;
	const navigate = useNavigate();
	const filters = parseEvaluationFilters(search);
	const anyActive = (list.data ?? []).some((e) => isActiveStatus(e.status));
	// A second subscription with polling only while something is still running.
	useListEvaluationsQuery(undefined, { pollingInterval: anyActive ? EVALUATION_POLL_MS : 0, skip: !anyActive });
	const setFilters = (next: EvaluationFilters) =>
		void navigate({ to: "/evaluations", search: evaluationFiltersSearch(next) as never, replace: true });
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="evaluations-screen">
			{/* No page action: the header's "Evaluate an agent" is the one way in (M3-54). */}
			<PageHeader title={t("list.title")} subtitle={t("list.subtitle")} />
			{list.isLoading ? (
				<BlockSkeleton rows={8} testId="evaluations-loading" />
			) : list.error || !list.data ? (
				<LoadError title={t("list.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="evaluations-error" />
			) : (
				<EvaluationTable evaluations={list.data} filters={filters} onFiltersChange={setFilters} />
			)}
		</div>
	);
}

const selectClass = "border-input bg-card dark:bg-input/20 h-8 rounded-md border px-2 text-xs shadow-xs outline-none";

/** Search, status, mode and sort above the table. */
function EvaluationToolbar({
	evaluations,
	filters,
	onChange,
	shown,
}: {
	evaluations: readonly Evaluation[];
	filters: EvaluationFilters;
	onChange: (next: EvaluationFilters) => void;
	shown: number;
}) {
	const { t } = useTranslation("evaluations");
	const { t: tf } = useTranslation("fw08");
	const modes: string[] = [...new Set(evaluations.map((e) => evaluationKind(e)).filter(Boolean))].sort();
	const filtered = filters.q !== "" || filters.status !== "all" || filters.mode !== "all";
	return (
		<div className="flex flex-wrap items-center gap-2" data-testid="evaluations-toolbar">
			<label className="relative">
				<span className="sr-only">{tf("list.search")}</span>
				<Search
					className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2"
					aria-hidden="true"
				/>
				<Input
					type="search"
					className="h-8 w-64 pl-7 text-xs"
					placeholder={t("list.searchPlaceholder")}
					aria-label={t("list.searchPlaceholder")}
					value={filters.q}
					onChange={(e) => onChange({ ...filters, q: e.target.value })}
					data-testid="evaluations-search"
				/>
			</label>
			<label className="flex items-center gap-1.5 text-xs">
				<span className="text-muted-foreground">{tf("list.status")}</span>
				<select
					className={selectClass}
					value={filters.status}
					onChange={(e) => onChange({ ...filters, status: e.target.value as EvaluationFilters["status"] })}
					data-testid="evaluations-status-filter"
				>
					{EVALUATION_STATUS_FILTERS.map((s) => (
						<option key={s} value={s}>
							{s === "all" ? tf("list.statusAll") : t(`status.${s}`)}
						</option>
					))}
				</select>
			</label>
			<label className="flex items-center gap-1.5 text-xs">
				<span className="text-muted-foreground">{tf("list.mode")}</span>
				<select
					className={selectClass}
					value={filters.mode}
					onChange={(e) => onChange({ ...filters, mode: e.target.value })}
					data-testid="evaluations-mode-filter"
				>
					<option value="all">{tf("list.modeAll")}</option>
					{(filters.mode !== "all" && !modes.includes(filters.mode) ? [...modes, filters.mode] : modes).map((m) => {
						const key = modeKey(m);
						return (
							<option key={m} value={m}>
								{m === FAILURE_TEST_MODE ? t("mode.failureTest") : key ? t(key) : t("mode.unknown", { mode: m })}
							</option>
						);
					})}
				</select>
			</label>
			<label className="flex items-center gap-1.5 text-xs">
				<span className="text-muted-foreground">{tf("list.sort")}</span>
				<select
					className={selectClass}
					value={filters.sort}
					onChange={(e) => onChange({ ...filters, sort: e.target.value as EvaluationFilters["sort"] })}
					data-testid="evaluations-sort"
				>
					{EVALUATION_SORTS.map((s) => (
						<option key={s} value={s}>
							{s === "result" ? t("list.sortResult") : tf(`list.sorts.${s}`)}
						</option>
					))}
				</select>
			</label>
			{filtered && (
				<Button
					variant="ghost"
					size="sm"
					className="h-8 text-xs"
					onClick={() => onChange({ ...filters, q: "", status: "all", mode: "all" })}
					dataTestId="evaluations-clear"
				>
					{tf("list.clear")}
				</Button>
			)}
			<span className="text-muted-foreground ml-auto text-xs tabular-nums" data-testid="evaluations-shown">
				{tf("list.shown", { shown, total: evaluations.length })}
			</span>
		</div>
	);
}

/** Names that more than one row shows, so those rows add their start date (fix wave 3, N297). */
function repeatedNames(rows: readonly Evaluation[]): Set<string> {
	const seen = new Map<string, number>();
	for (const ev of rows) seen.set(ev.spec.name || ev.id, (seen.get(ev.spec.name || ev.id) ?? 0) + 1);
	return new Set([...seen].filter(([, n]) => n > 1).map(([name]) => name));
}

/** The loaded list with its toolbar. Exported for tests. */
export function EvaluationTable({
	evaluations,
	filters = parseEvaluationFilters({}),
	onFiltersChange = () => undefined,
}: {
	evaluations: readonly Evaluation[];
	filters?: EvaluationFilters;
	onFiltersChange?: (next: EvaluationFilters) => void;
}) {
	const { t, i18n } = useTranslation("evaluations");
	const { t: tf } = useTranslation("fw08");
	const [picked, setPicked] = useState<Set<string>>(() => new Set());
	const [deleting, setDeleting] = useState(false);
	const [bulkError, setBulkError] = useState<string | null>(null);
	if (evaluations.length === 0) return <EmptyNote testId="evaluations-empty">{t("list.empty")}</EmptyNote>;
	const rows = filterEvaluations(evaluations, filters);
	const repeated = repeatedNames(rows);
	const deletable = rows.filter((e) => !isActiveStatus(e.status));
	const toggle = (id: string) =>
		setPicked((prev) => {
			const next = new Set(prev);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	return (
		<div className="flex flex-col gap-3">
			<EvaluationToolbar evaluations={evaluations} filters={filters} onChange={onFiltersChange} shown={rows.length} />
			{rows.length === 0 ? (
				<EmptyNote testId="evaluations-no-match">{tf("list.noMatch")}</EmptyNote>
			) : (
				<>
				{picked.size > 0 && (
					<div className="flex flex-wrap items-center gap-3" data-testid="evaluations-bulk">
						<BulkDelete ids={[...picked]} busy={deleting} onBusy={setDeleting} onDone={(failed, error) => {
							setPicked(new Set(failed));
							setBulkError(failed.length ? t("list.deleteSelectedError", { count: failed.length, error }) : null);
						}} />
						{bulkError && (
							<span className="text-chart-error-ink text-xs" role="alert">
								{bulkError}
							</span>
						)}
					</div>
				)}
				<Table data-testid="evaluations-table">
					<caption className="sr-only">{t("list.caption")}</caption>
					<TableHeader>
						<TableRow>
							<TableHead className="w-8">
								<input
									type="checkbox"
									className="accent-primary relative z-10 size-4"
									aria-label={t("list.selectAll")}
									checked={deletable.length > 0 && deletable.every((e) => picked.has(e.id))}
									onChange={(e) => setPicked(e.target.checked ? new Set(deletable.map((d) => d.id)) : new Set())}
									data-testid="evaluations-select-all"
								/>
							</TableHead>
							<TableHead>{t("list.col.name")}</TableHead>
							<TableHead>{t("list.col.status")}</TableHead>
							<TableHead className="hidden text-right sm:table-cell">{t("list.col.result")}</TableHead>
							<TableHead className="hidden text-right sm:table-cell">{t("list.col.spend")}</TableHead>
							<TableHead className="hidden sm:table-cell">{t("list.col.created")}</TableHead>
							<TableHead className="hidden xl:table-cell">{t("list.col.agents")}</TableHead>
							<TableHead className="hidden xl:table-cell">{t("list.col.mode")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((ev) => {
							const benches = evaluationBenchmarks(ev);
							const status = evaluationStatusWord(ev);
							const started = formatCreated(ev.created_at, i18n.language);
							return (
								// The whole row opens the evaluation: the name link covers it (M3-51).
								<TableRow key={ev.id} className="hover:bg-muted/50 relative" data-testid={`evaluation-row-${ev.id}`}>
									<TableCell>
										{!isActiveStatus(ev.status) && (
											<input
												type="checkbox"
												className="accent-primary relative z-10 size-4"
												aria-label={t("list.selectRow", { name: ev.spec.name || ev.id })}
												checked={picked.has(ev.id)}
												onChange={() => toggle(ev.id)}
												data-testid={`evaluation-select-${ev.id}`}
											/>
										)}
									</TableCell>
									<TableCell className="font-medium">
										<Link
											to="/evaluations/$evalId"
											params={{ evalId: ev.id }}
											className="after:absolute after:inset-0 hover:underline"
											data-testid={`evaluation-open-${ev.id}`}
										>
											{ev.spec.name || ev.id}
										</Link>
										<span
											className="text-muted-foreground block max-w-80 truncate text-xs font-normal"
											title={benches.join(", ")}
											data-testid={`evaluation-benchmarks-${ev.id}`}
										>
											{benches.join(", ")}
										</span>
										{/* Phones: result, spend and start on a muted second line. */}
										<span className="text-muted-foreground block text-xs font-normal tabular-nums sm:hidden">
											{[evaluationResultText(ev), usd(evaluationSpend(ev)), started]
												.filter(Boolean)
												.join(" \u00b7 ")}
										</span>
									</TableCell>
									<TableCell>
										{status === "notStarted" ? (
											<EvaluationStatusPill status="notStarted" testId={`evaluation-status-${ev.id}`} />
										) : (
											<EvaluationLivePill evaluation={ev} testId={`evaluation-status-${ev.id}`} />
										)}
									</TableCell>
									<TableCell className="hidden text-right tabular-nums sm:table-cell" data-testid={`evaluation-result-${ev.id}`}>
										{evaluationResultText(ev)}
									</TableCell>
									<TableCell className="hidden text-right tabular-nums sm:table-cell" data-testid={`evaluation-spend-${ev.id}`}>
										{usd(evaluationSpend(ev))}
									</TableCell>
									<TableCell className="text-muted-foreground hidden tabular-nums sm:table-cell">
										{started}
										{ev.schedule && (
											<span
												className="ml-2 inline-flex items-center gap-1 text-xs"
												title={t("detail.scheduledLine", { name: ev.spec.name || ev.id, schedule: ev.schedule, date: started })}
												data-testid={`evaluation-scheduled-${ev.id}`}
											>
												<CalendarClock className="size-3.5" aria-hidden="true" />
												{t("list.nightly")}
											</span>
										)}
									</TableCell>
									<TableCell className="hidden xl:table-cell">{evaluationAgents(ev).join(", ")}</TableCell>
									<TableCell className="hidden xl:table-cell">
										<EvaluationKindLabel evaluation={ev} />
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
				</>
			)}
		</div>
	);
}
/** Bulk delete (X-75): one confirm, then each picked evaluation; the ones that failed stay picked. */
function BulkDelete({
	ids,
	busy,
	onBusy,
	onDone,
}: {
	ids: string[];
	busy: boolean;
	onBusy(b: boolean): void;
	onDone(failed: string[], error: string): void;
}) {
	const { t } = useTranslation("evaluations");
	const confirm = useConfirm();
	const [remove] = useDeleteEvaluationMutation();
	const run = async () => {
		const ok = await confirm(t("list.deleteSelectedBody"), {
			title: t("list.deleteSelectedTitle", { count: ids.length }),
			confirmLabel: t("list.deleteSelectedOk"),
			cancelLabel: t("detail.deleteKeep"),
			destructive: true,
		});
		if (!ok) return;
		onBusy(true);
		const failed: string[] = [];
		let lastError = "";
		for (const id of ids) {
			try {
				await remove(id).unwrap();
			} catch (e) {
				failed.push(id);
				lastError = getBenchErrorMessage(e);
			}
		}
		onBusy(false);
		onDone(failed, lastError);
	};
	return (
		<Button size="sm" variant="destructive" disabled={busy} onClick={() => void run()} data-testid="evaluations-delete-selected">
			{t("list.deleteSelected", { count: ids.length })}
		</Button>
	);
}
