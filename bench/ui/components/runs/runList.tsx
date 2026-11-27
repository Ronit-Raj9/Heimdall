import { compareSearch } from "@/components/experiments/legacyRoutes";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { FilterBar, FilterSelect } from "@/components/ui/toolbar";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { getLocale } from "@/lib/i18n";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import type { RunListFacets, RunListParams, RunListRow } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ChevronLeft, ChevronRight, Gavel, GitCompareArrows, ListX } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useTranslation } from "react-i18next";
import { OutcomeBadge } from "./outcomeBadge";
import { INFRA_OUTCOMES, shortReason } from "./runFailure.utils";
import { BlockSkeleton, LoadError } from "./screenStates";
import "@/app/runs/views/w4m4I18n";
import "@/components/verdict/uf4aI18n";

/** A run row with the task title the server may add (G1, M4-18); optional, today's id stays the fallback. */
type TitledRow = RunListRow & { task_title?: string };

/** True when every scored run is 0 or 1, so the Score column only repeats the Result (M4-21). Pure. */
export function passFailOnly(rows: readonly RunListRow[]): boolean {
	const scored = rows.filter((row) => row.y?.v != null);
	return scored.length > 0 && scored.every((row) => row.y.v === 0 || row.y.v === 1);
}

/** The rows of one comparison by task, then setup, so A and B of a task sit together (M4-22). Pure. */
export function byTaskThenSetup<T extends Pick<RunListRow, "task" | "arm" | "trial">>(rows: readonly T[]): T[] {
	return [...rows].sort(
		(x, y) => x.task.localeCompare(y.task, undefined, { numeric: true }) || x.arm.localeCompare(y.arm) || x.trial - y.trial,
	);
}

/** True when a filter offers a choice: two or more values, or one already chosen (M4-19). Pure. */
export function filterHasChoice(options: readonly string[], value: string | null | undefined): boolean {
	return Boolean(value) || options.length > 1;
}

/** On a comparison, the filters shown at once (M4-19); the rest go behind More filters. */
export const MAIN_FILTERS: ReadonlySet<string> = new Set(["arm", "outcome"]);

/** A start time as month, day and time; the year is left out to keep the column narrow. */
export function formatShortTime(iso: string | undefined, locale: string): string {
	if (!iso) return "";
	const at = new Date(iso);
	if (Number.isNaN(at.getTime())) return "";
	return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(at);
}

/**
 * The last page number when page is past the end of a list of total rows, else null (no rows at
 * all, or the page has rows). A page past the end says so and offers the last page.
 */
export function pastLastPage(total: number, page: number, size: number): number | null {
	if (total === 0) return null;
	const last = Math.ceil(total / size);
	return page > last ? last : null;
}

/** Rows per page of the run list. */
export const RUN_LIST_PAGE = 50;

/** The filters the run list offers, in menu order, each with its facet and its query parameter. */
export const RUN_FILTERS = [
	{ param: "exp", facet: "experiments" },
	{ param: "arm", facet: "arms" },
	{ param: "task", facet: "tasks" },
	{ param: "condition", facet: "conditions" },
	{ param: "outcome", facet: "outcomes" },
	{ param: "track", facet: "tracks" },
] as const satisfies readonly { param: keyof RunListParams; facet: keyof RunListFacets }[];

type FilterParam = (typeof RUN_FILTERS)[number]["param"];

const searchParsers = {
	exp: parseAsString,
	arm: parseAsString,
	task: parseAsString,
	condition: parseAsString,
	outcome: parseAsString,
	track: parseAsString,
	page: parseAsInteger,
};

/** The request for one page of the list: empty filters left out, the page turned into an offset. */
export function runListRequest(filters: Partial<Record<FilterParam, string | null>>, page: number | null): RunListParams {
	const params: RunListParams = { limit: RUN_LIST_PAGE };
	for (const { param } of RUN_FILTERS) {
		const value = filters[param];
		if (value) params[param] = value;
	}
	const p = Math.max(1, page ?? 1);
	if (p > 1) params.offset = (p - 1) * RUN_LIST_PAGE;
	return params;
}

export interface RunListProps {
	/** An experiment page passes its id: the experiment filter and column are then left out. */
	expId?: string;
	/** The comparison is still running: an empty list says runs are on their way, with no action. */
	running?: boolean;
	testId?: string;
}

/**
 * Runs across experiments (GET /runs), newest first. Every filter lives in the URL, so a filtered
 * list can be shared; the menus offer the values the server has seen. Scores are each run's sealed
 * grade, and the header says so.
 */
export function RunList({ expId, running = false, testId = "run-list" }: RunListProps) {
	const { t } = useTranslation("bench");
	const { t: tw } = useTranslation("w4m4");
	const [search, setSearch] = useQueryStates(searchParsers, { history: "replace" });
	const filters = { ...search, exp: expId ?? search.exp };
	const request = runListRequest(filters, search.page);
	const list = useListRunsQuery(request);
	const shownFilters = RUN_FILTERS.filter((f) => !(expId && f.param === "exp"));
	// M4-19: a filter with one value is no choice and is left out; on a comparison only Setup and Result show at once.
	const choosable = shownFilters.filter((f) => !list.data || filterHasChoice(list.data.facets[f.facet] ?? [], search[f.param]));
	const active = shownFilters.some((f) => Boolean(search[f.param]));
	const page = Math.max(1, search.page ?? 1);

	const setFilter = (param: FilterParam, value: string | null) => void setSearch({ [param]: value, page: null });
	const clear = () => void setSearch({ exp: null, arm: null, task: null, condition: null, outcome: null, track: null, page: null });

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid={testId}>
			<FilterBar label={t("runs.list.filtersLabel")} testId="run-list-filters">
				{choosable.map((filter) => (
					<RunFilterSelect
						key={filter.param}
						param={filter.param}
						options={list.data?.facets[filter.facet] ?? []}
						value={search[filter.param]}
						onChange={(value) => setFilter(filter.param, value)}
					/>
				))}
				{active && (
					<Button variant="ghost" size="sm" onClick={clear} dataTestId="run-list-clear">
						{t("runs.list.clear")}
					</Button>
				)}
			</FilterBar>

			{list.isLoading ? (
				<BlockSkeleton rows={8} testId="run-list-loading" />
			) : list.error || !list.data ? (
				<LoadError title={t("runs.list.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="run-list-error" />
			) : list.data.runs.length === 0 && pastLastPage(list.data.total, page, RUN_LIST_PAGE) !== null ? (
				<div className="flex flex-col gap-3" data-testid="run-list-past-end">
					<p className="text-muted-foreground text-sm">{tw("runs.pastEnd")}</p>
					<div>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								const last = pastLastPage(list.data?.total ?? 0, page, RUN_LIST_PAGE) ?? 1;
								void setSearch({ page: last === 1 ? null : last });
							}}
							dataTestId="run-list-last-page"
						>
							{tw("runs.lastPage")}
						</Button>
					</div>
					<Pager page={page} total={list.data.total} onPage={(next) => void setSearch({ page: next === 1 ? null : next })} />
				</div>
			) : list.data.runs.length === 0 && running && !active ? (
				<EmptyState icon={ListX} message={tw("runs.running")} testId="run-list-running" />
			) : list.data.runs.length === 0 ? (
				<EmptyState
					icon={ListX}
					message={active ? t("runs.list.noMatch") : t("runs.list.empty")}
					action={
						active ? { label: t("runs.list.clear"), onClick: clear } : { label: t("runs.list.newExperiment"), to: "/experiments/new" }
					}
					testId="run-list-empty"
				/>
			) : (
				<>
					<Card className="hidden py-0 shadow-none md:flex">
						<RunTable rows={expId ? byTaskThenSetup(list.data.runs) : list.data.runs} showExperiment={!expId} banded={Boolean(expId)} />
					</Card>
					<RunCards rows={expId ? byTaskThenSetup(list.data.runs) : list.data.runs} showExperiment={!expId} />
					<Pager page={page} total={list.data.total} onPage={(next) => void setSearch({ page: next === 1 ? null : next })} />
				</>
			)}
		</div>
	);
}

function RunFilterSelect({
	param,
	options,
	value,
	onChange,
}: {
	param: FilterParam;
	options: readonly string[];
	value: string | null;
	onChange: (value: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	return (
		<FilterSelect
			label={t(`runs.list.filter.${param}`)}
			options={options.map((option) => ({
				value: option,
				label: param === "outcome" ? t(`runs.outcome.${option}`, { defaultValue: option }) : option,
			}))}
			value={value}
			onChange={onChange}
			allLabel={t("runs.list.all")}
			testId={`run-filter-${param}`}
		/>
	);
}

/** The run table. Every id links to its page; the pair opens the side-by-side comparison. */
export function RunTable({
	rows,
	showExperiment = true,
	banded = false,
}: {
	rows: readonly TitledRow[];
	showExperiment?: boolean;
	/** Alternate a light band per task, so the setups of one task read as a group (M4-22). */
	banded?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4a");
	const locale = getLocale();
	const showScore = !passFailOnly(rows);
	let band = 0;
	return (
		<Table data-testid="run-table" aria-label={t("runs.list.tableLabel")}>
			<TableHeader>
				<TableRow>
					<TableHead scope="col">{t("runs.list.col.run")}</TableHead>
					{showExperiment && <TableHead scope="col">{t("runs.list.col.experiment")}</TableHead>}
					<TableHead scope="col">{t("runs.list.col.task")}</TableHead>
					<TableHead scope="col">{t("runs.list.col.arm")}</TableHead>
					<TableHead scope="col">{t("runs.list.col.condition")}</TableHead>
					<TableHead scope="col">{t("runs.list.col.outcome")}</TableHead>
					{/* M4-21: a pass or fail benchmark has no Score column; the Result says it all. */}
					{showScore && (
						<TableHead scope="col" className="text-right">
							<span className="inline-flex items-center gap-1.5">
								{t("runs.list.col.score")}
								<GradeSourceTag sealedOnly judged={rows.every((row) => row.grading === "judged")} testId="run-table-score-source" />
							</span>
						</TableHead>
					)}
					<TableHead scope="col" className="text-right">
						{t("runs.list.col.cost")}
					</TableHead>
					<TableHead scope="col">{t("runs.list.col.started")}</TableHead>
					<TableHead scope="col">
						<span className="sr-only">{t("runs.list.col.compare")}</span>
					</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row, i) => {
					if (i > 0 && rows[i - 1].task !== row.task) band += 1;
					return (
						<TableRow
							key={row.run_id}
							data-testid={`run-row-${row.run_id}`}
							data-arm={row.arm}
							className={banded && band % 2 === 1 ? "bg-muted/40" : undefined}
						>
							<TableCell className="text-xs">
								<EntityLink kind="run" id={row.run_id} testId={`run-row-${row.run_id}-link`} />
								{!row.counted && <span className="text-muted-foreground ml-1.5 text-[11px]">{t("runs.list.notCounted")}</span>}
								{row.attempt > 1 && (
									<span className="text-muted-foreground ml-1.5 text-[11px]">{t("runs.list.attempt", { n: row.attempt })}</span>
								)}
							</TableCell>
							{showExperiment && (
								<TableCell className="text-xs">
									<EntityLink kind="experiment" id={row.exp_id} />
								</TableCell>
							)}
							<TableCell className="text-xs">
								<TaskCell row={row} />
							</TableCell>
							<TableCell className="font-mono text-xs font-semibold">{row.arm}</TableCell>
							<TableCell className="text-xs">
								{/* One text "clean, attempt 1" so copied or read-aloud text never runs the two together (Q-255). */}
								<span className="whitespace-nowrap" title={t("runs.list.trialHint", { trial: row.trial })} data-testid={`run-row-${row.run_id}-fault`}>
									{faultText(row.condition, t("runs.list.trial", { trial: row.trial }))}
								</span>
							</TableCell>
							<TableCell>
								{row.sealed ? <RowOutcome row={row} /> : <span className="text-chart-info-ink text-xs">{t("runs.list.inFlight")}</span>}
							</TableCell>
							{showScore && (
								<TableCell className="text-right">
									<span className="inline-flex items-center gap-1">
										{row.grading === "judged" && (
											<Gavel
												className="text-chart-warning-ink size-3"
												aria-label={t("verdict.source.kind.judged")}
												data-testid={`run-row-${row.run_id}-judged`}
											/>
										)}
										<ValueCell value={row.y} kind="count" />
									</span>
								</TableCell>
							)}
							<TableCell className="text-right">
								<ValueCell value={row.cost_usd} kind="usd" />
							</TableCell>
							<TableCell className="text-muted-foreground text-xs whitespace-nowrap tabular-nums">
								{formatShortTime(row.started, locale)}
							</TableCell>
							{/* M4-20: the icon column never clips; a 40 px target on touch screens. */}
							<TableCell className="w-12 min-w-12 text-right">
								{row.pair_id && row.paired && (
									<Button variant="ghost" size="icon" className="size-10 sm:size-8" asChild>
										<Link
											to="/compare"
											search={compareSearch(row.pair_id, row.exp_id) as never}
											aria-label={tu("runs.compareTask", { task: row.task_title || row.task })}
											title={tu("runs.compareTask", { task: row.task_title || row.task })}
											data-testid={`run-row-${row.run_id}-compare`}
										>
											<GitCompareArrows className="size-4" aria-hidden="true" />
										</Link>
									</Button>
								)}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}

/** The task: its title when the server sends one (M4-18), with the id as a tooltip; else the id link. */
/** The Fault cell's text: the condition and the attempt, "clean, attempt 1" (Q-255). Pure, exported for tests. */
export function faultText(condition: string, attempt: string): string {
	return condition ? `${condition}, ${attempt}` : attempt;
}

function TaskCell({ row }: { row: TitledRow }) {
	if (!row.task_title) return <EntityLink kind="task" id={row.task} suite={row.suite} />;
	// The title is the link; the id stays in its tooltip (Q-30), not on a second line.
	return (
		<span className="block max-w-80 min-w-0 truncate" data-testid={`run-row-${row.run_id}-task-title`}>
			<EntityLink kind="task" id={row.task} suite={row.suite} className="font-sans text-sm">
				{row.task_title}
			</EntityLink>
		</span>
	);
}

/** The outcome badge; an infra error also shows its short reason, with the full first line as a tooltip. */
export function RowOutcome({ row, testId = true }: { row: RunListRow; testId?: boolean }) {
	const reason =
		row.excluded === "out_of_memory" ? "excluded: out of memory" : INFRA_OUTCOMES.includes(row.outcome) ? shortReason(row.error, 80) : "";
	const badge = <OutcomeBadge outcome={row.outcome} testId={testId ? `run-row-${row.run_id}-outcome` : undefined} />;
	if (!reason) return badge;
	return (
		<span className="flex max-w-56 min-w-0 flex-col items-start gap-0.5" title={shortReason(row.error, 600)}>
			{badge}
			<span className="text-muted-foreground w-full truncate text-[11px]" data-testid={`run-row-${row.run_id}-reason`}>
				{reason}
			</span>
		</span>
	);
}

/** The run list on a phone: one card per run with the same links, in place of the wide table. */
function RunCards({ rows, showExperiment }: { rows: readonly TitledRow[]; showExperiment: boolean }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<ul className="flex flex-col gap-2 md:hidden" data-testid="run-cards" aria-label={t("runs.list.tableLabel")}>
			{rows.map((row) => (
				<li
					key={row.run_id}
					className="bg-card flex flex-col gap-1.5 rounded-lg border px-3 py-2.5 text-xs"
					data-testid={`run-card-${row.run_id}`}
				>
					<div className="flex items-center justify-between gap-2">
						<EntityLink kind="run" id={row.run_id} />
						{row.sealed ? <RowOutcome row={row} testId={false} /> : <span className="text-chart-info-ink">{t("runs.list.inFlight")}</span>}
					</div>
					<div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1">
						<span>
							{t("runs.list.col.task")} <TaskCell row={row} />
						</span>
						<span>
							{t("runs.list.col.arm")} <span className="text-foreground font-mono font-semibold">{row.arm}</span>
						</span>
						<span>{faultText(row.condition, t("runs.list.trial", { trial: row.trial }))}</span>
					</div>
					<div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1">
						{showExperiment && <EntityLink kind="experiment" id={row.exp_id} />}
						<span className="tabular-nums">{formatShortTime(row.started, locale)}</span>
						<span className="ml-auto flex items-center gap-2">
							<ValueCell value={row.cost_usd} kind="usd" />
							{row.pair_id && row.paired && (
								<Link
									to="/compare"
									search={compareSearch(row.pair_id, row.exp_id) as never}
									className="text-muted-foreground hover:text-primary inline-flex items-center"
									aria-label={t("runs.list.compareAria", { run: row.run_id })}
								>
									<GitCompareArrows className="size-3.5" aria-hidden="true" />
								</Link>
							)}
						</span>
					</div>
				</li>
			))}
		</ul>
	);
}

function Pager({ page, total, onPage }: { page: number; total: number; onPage: (page: number) => void }) {
	const { t } = useTranslation("bench");
	const pages = Math.max(1, Math.ceil(total / RUN_LIST_PAGE));
	const first = (page - 1) * RUN_LIST_PAGE + 1;
	const last = Math.min(total, page * RUN_LIST_PAGE);
	return (
		<nav
			className="flex flex-wrap items-center justify-between gap-2 text-xs"
			aria-label={t("runs.list.pages")}
			data-testid="run-list-pager"
		>
			<span className="text-muted-foreground tabular-nums" data-testid="run-list-count">
				{t("runs.list.showing", { first, last, total })}
			</span>
			{pages > 1 && (
				<span className="flex items-center gap-1">
					<Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)} dataTestId="run-list-prev">
						<ChevronLeft aria-hidden="true" />
						{t("runs.list.prev")}
					</Button>
					<span className="text-muted-foreground px-2 tabular-nums">{t("runs.list.page", { page, pages })}</span>
					<Button variant="outline" size="sm" disabled={page >= pages} onClick={() => onPage(page + 1)} dataTestId="run-list-next">
						{t("runs.list.next")}
						<ChevronRight aria-hidden="true" />
					</Button>
				</span>
			)}
		</nav>
	);
}