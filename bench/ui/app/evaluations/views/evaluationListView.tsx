import "@/app/evaluations_/$evalId/views/fw08I18n";
import { PageHeader } from "@/components/shell/pageHeader";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
	EVALUATION_POLL_MS,
	EVALUATION_SORTS,
	EVALUATION_STATUS_FILTERS,
	FAILURE_TEST_MODE,
	evaluationAgents,
	evaluationBenchmarks,
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
import { EvaluationKindLabel } from "./evaluationWords";
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
			<PageHeader
				title={t("list.title")}
				subtitle={t("list.subtitle")}
				actions={
					<Button asChild size="sm">
						<Link to={"/evaluate" as never} data-testid="evaluations-new">
							<Plus className="size-4" aria-hidden="true" />
							{t("list.new")}
						</Link>
					</Button>
				}
			/>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="evaluations-loading" />
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
					placeholder={tf("list.searchPlaceholder")}
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
	if (evaluations.length === 0) return <EmptyNote testId="evaluations-empty">{t("list.empty")}</EmptyNote>;
	const rows = filterEvaluations(evaluations, filters);
	const repeated = repeatedNames(rows);
	return (
		<div className="flex flex-col gap-3">
			<EvaluationToolbar evaluations={evaluations} filters={filters} onChange={onFiltersChange} shown={rows.length} />
			{rows.length === 0 ? (
				<EmptyNote testId="evaluations-no-match">{tf("list.noMatch")}</EmptyNote>
			) : (
				<Table data-testid="evaluations-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("list.col.name")}</TableHead>
							<TableHead>{t("list.col.agents")}</TableHead>
							<TableHead>{t("list.col.mode")}</TableHead>
							<TableHead>{t("list.col.benchmarks")}</TableHead>
							<TableHead>{t("list.col.status")}</TableHead>
							<TableHead className="text-right">{t("list.col.result")}</TableHead>
							<TableHead className="text-right">{t("list.col.spend")}</TableHead>
							<TableHead>{t("list.col.created")}</TableHead>
							<TableHead className="sr-only">{t("list.col.open")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((ev) => {
							const benches = evaluationBenchmarks(ev);
							return (
								<TableRow key={ev.id} data-testid={`evaluation-row-${ev.id}`}>
									<TableCell className="font-medium">
										<Link to="/evaluations/$evalId" params={{ evalId: ev.id }} className="hover:underline">
											{ev.spec.name || ev.id}
										</Link>
										{repeated.has(ev.spec.name || ev.id) && (
											<span
												className="text-muted-foreground ml-2 text-xs font-normal tabular-nums"
												data-testid={`evaluation-name-date-${ev.id}`}
											>
												{formatCreated(ev.created_at, i18n.language)}
											</span>
										)}
										{ev.schedule && (
											<Badge
												variant="outline"
												className="ml-2 text-xs font-normal"
												title={t("detail.scheduledLine", {
													name: ev.spec.name || ev.id,
													schedule: ev.schedule,
													date: formatCreated(ev.created_at, i18n.language),
												})}
												data-testid={`evaluation-scheduled-${ev.id}`}
											>
												{t("detail.scheduled")}
											</Badge>
										)}
										<span
											className="text-muted-foreground block max-w-80 truncate text-xs font-normal"
											title={benches.join(", ")}
											data-testid={`evaluation-benchmarks-${ev.id}`}
										>
											{benches.join(", ")}
										</span>
									</TableCell>
									<TableCell>{evaluationAgents(ev).join(", ")}</TableCell>
									<TableCell>
										<EvaluationKindLabel evaluation={ev} />
									</TableCell>
									<TableCell className="tabular-nums">{t("list.benchmarkCount", { count: benches.length })}</TableCell>
									<TableCell>
										<EvaluationLivePill evaluation={ev} testId={`evaluation-status-${ev.id}`} />
									</TableCell>
									<TableCell className="text-right tabular-nums" data-testid={`evaluation-result-${ev.id}`}>
										{evaluationResultText(ev)}
									</TableCell>
									<TableCell className="text-right tabular-nums" data-testid={`evaluation-spend-${ev.id}`}>
										{usd(evaluationSpend(ev))}
									</TableCell>
									<TableCell className="text-muted-foreground tabular-nums">{formatCreated(ev.created_at, i18n.language)}</TableCell>
									<TableCell className="text-right">
										<Link
											to="/evaluations/$evalId"
											params={{ evalId: ev.id }}
											className="text-primary text-sm hover:underline"
											data-testid={`evaluation-open-${ev.id}`}
										>
											{t("list.openOne")}
										</Link>
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			)}
		</div>
	);
}