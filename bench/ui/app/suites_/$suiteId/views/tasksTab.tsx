import { EmptyNote } from "@/components/runs/screenStates";
import type { HealthState } from "@/components/suites/suiteHealth.utils";
import { SuiteTaskFilters } from "@/components/suites/suiteTaskFilters";
import { SuiteTaskTable } from "@/components/suites/suiteTaskTable";
import {
	ADMISSION_STATES,
	DEFAULT_TASK_SORT,
	SORT_DIRS,
	filterOptions,
	filterTasks,
	nextTaskSort,
	parseTaskSort,
	sortTasks,
	type TaskFilters,
	type TaskSortKey,
} from "@/components/suites/suites.utils";
import { ReadinessChecklist } from "@/components/suites/readinessChecklist";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { searchTasks, taskPage } from "@/components/suites/taskPaging";
import "@/components/suites/uf6aI18n";
import { useTaskCategories } from "@/components/suites/taskCategoryCell";
import { TaskDetailPanel } from "@/components/suites/taskDetailPanel";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsInteger, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { Columns3 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Pager } from "@/components/ui/pager";
import { useTranslation } from "react-i18next";
import "./gw13I18n";

/** Something that can scroll itself into view (an element, or a stub in tests). */
interface Scrollable {
	scrollIntoView(arg?: ScrollIntoViewOptions): void;
}

/**
 * N163: brings the task opened from a link into view, once per task id: the detail panel to the top of the
 * page and its row inside the table. seen holds the last task id handled, so a re-render does not scroll again.
 */
export function revealOpenTask(taskId: string | null, seen: { current: string | null }, panel: Scrollable | null, row: Scrollable | null) {
	if (!taskId || seen.current === taskId) return;
	seen.current = taskId;
	panel?.scrollIntoView({ block: "start" });
	row?.scrollIntoView({ block: "nearest" });
}

/** URL state of the task table: filters, sort and the open task. */
export const TASK_QUERY = {
	kind: parseAsString,
	difficulty: parseAsString,
	cap: parseAsString,
	status: parseAsStringLiteral(ADMISSION_STATES),
	sort: parseAsString,
	dir: parseAsStringLiteral(SORT_DIRS),
	task: parseAsString,
	/** M6-1: search text (task id or title) and the 1-based page of 50 rows. */
	q: parseAsString,
	page: parseAsInteger,
	/** M1-12: the version a run used, sent by run and compare links with ?task=. */
	rev: parseAsString,
};

/** Tasks tab: the filterable task table, the task in ?task= open beside it. */
export function SuiteTasksTab({ suite, health }: { suite: SuiteSummary; health: HealthState }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("suitesUx");
	const { t: tg } = useTranslation("gw13");
	const { t: tf } = useTranslation("uf6a");
	const [q, setQ] = useQueryStates(TASK_QUERY);
	const session = useSession();
	// M6-12: an own benchmark that is not fully ready shows the three steps above the table.
	const showChecklist = !isBuiltinSuite(suite) && suite.tasks.length > 0 && suite.admitted_count < suite.task_count;
	const filters: TaskFilters = {
		kind: q.kind,
		difficulty: q.difficulty,
		capability: q.cap,
		status: q.status,
	};
	const sort = parseTaskSort(q.sort, q.dir);
	const options = useMemo(() => filterOptions(suite.tasks), [suite.tasks]);
	const rows = useMemo(
		() =>
			sortTasks(
				filterTasks(searchTasks(suite.tasks, q.q), {
					kind: q.kind,
					difficulty: q.difficulty,
					capability: q.cap,
					status: q.status,
				}),
				parseTaskSort(q.sort, q.dir),
			),
		[suite.tasks, q.q, q.kind, q.difficulty, q.cap, q.status, q.sort, q.dir],
	);
	// Expert columns (kind, skills tested, license, hidden checks) stay behind a toggle.
	const [moreColumns, setMoreColumns] = useState(false);
	const categories = useTaskCategories(suite, moreColumns);
	const openTask = q.task && suite.tasks.some((task) => task.id === q.task) ? q.task : null;
	// A ?task= the benchmark does not have (removed in this version) is named, not dropped silently.
	const missingTask = q.task && !openTask ? q.task : null;
	// M6-1: one page of 50 rows; without a page in the URL the linked task's page opens.
	const paged = useMemo(() => taskPage(rows, q.page, openTask), [rows, q.page, openTask]);
	// N163: the task in the link (first render, not a later click) is scrolled into view with its row.
	const panelRef = useRef<HTMLDivElement>(null);
	const seenTask = useRef<string | null>(null);
	const fromLink = useRef(openTask);
	useEffect(() => {
		if (!fromLink.current || fromLink.current !== openTask) return;
		const row = document.querySelector<HTMLElement>(`[data-task-id="${CSS.escape(openTask)}"]`);
		revealOpenTask(openTask, seenTask, panelRef.current, row);
	}, [openTask]);

	const onSort = (key: TaskSortKey) => {
		const next = nextTaskSort(sort, key);
		const isDefault = next.key === DEFAULT_TASK_SORT.key && next.dir === DEFAULT_TASK_SORT.dir;
		void setQ({
			sort: isDefault ? null : next.key,
			dir: isDefault ? null : next.dir,
			page: null,
		});
	};

	return (
		<div className={cn("grid min-w-0 grid-cols-1 gap-4", openTask && "2xl:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]")}>
			{/* N163: the open task comes first in the page (above the table) below 2xl; beside it at 2xl. */}
			{openTask && (
				<div
					ref={panelRef}
					className="min-w-0 scroll-mt-4 2xl:sticky 2xl:top-4 2xl:order-last 2xl:self-start"
					data-testid="suites-task-detail-wrap"
				>
					{q.rev && (
						<p className="text-muted-foreground mb-2 text-xs" data-testid="suites-task-used-version">
							{tf("tasks.usedVersion", { rev: q.rev })}
						</p>
					)}
					<TaskDetailPanel
						suiteId={suite.id}
						suiteVersion={suite.version}
						taskId={openTask}
						suiteOutput={suite.output}
						suiteArtifacts={suite.artifacts}
						onClose={() => void setQ({ task: null, rev: null })}
						health={health}
						quarantine={suite.tasks.find((task) => task.id === openTask)?.quarantine ?? null}
					/>
				</div>
			)}
			{/* X-44: no second "Tasks" heading under the active Tasks tab; the description stays as a hint. */}
			<section className="flex min-w-0 flex-col gap-3" aria-label={tu("tasksTitle")} data-testid="suites-tasks">
				{showChecklist && (
					<ReadinessChecklist suite={suite} writable={canUse(session, "member")} hosted={session.authMode === "hosted"} />
				)}
				<p className="text-muted-foreground max-w-prose text-sm">{tu("tasksDescription")}</p>
				<SuiteTaskFilters
					filters={filters}
					options={options}
					search={q.q ?? ""}
					onSearch={(next) => void setQ({ q: next.trim() ? next : null, page: null })}
					onChange={(next) =>
						void setQ({
							kind: next.kind,
							difficulty: next.difficulty,
							cap: next.capability,
							status: next.status,
							page: null,
						})
					}
					onReset={() => void setQ({ kind: null, difficulty: null, cap: null, status: null, q: null, page: null })}
					trailing={
						suite.tasks.length > 0 && (
							<Button
								size="sm"
								variant="ghost"
								aria-pressed={moreColumns}
								aria-label={moreColumns ? tf("tasks.fewerColumns") : tf("tasks.moreColumns")}
								title={moreColumns ? tf("tasks.fewerColumns") : tf("tasks.moreColumns")}
								onClick={() => setMoreColumns((v) => !v)}
								data-testid="suites-tasks-more-columns"
							>
								<Columns3 className="size-4" aria-hidden="true" />
								{tf("tasks.columns")}
							</Button>
						)
					}
				/>
				{missingTask && (
					<div
						role="status"
						className="bg-muted/40 flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm"
						data-testid="suites-task-missing"
					>
						<span>
							{tg("tasks.missing", {
								task: missingTask,
								version: suite.version,
							})}
						</span>
						<Button size="sm" variant="ghost" onClick={() => void setQ({ task: null })} data-testid="suites-task-missing-dismiss">
							{tg("tasks.missingDismiss")}
						</Button>
					</div>
				)}
				{suite.tasks.length === 0 ? (
					<EmptyNote testId="suites-tasks-empty">{t("suites.tasks.empty")}</EmptyNote>
				) : rows.length === 0 ? (
					<EmptyNote testId="suites-tasks-no-match">{t("suites.tasks.noMatch")}</EmptyNote>
				) : (
					<>
						{/* M2-112a: keyboard users can jump past the table to the pager. */}
						<a
							href="#suites-task-pager"
							className="focus-visible:ring-(--focus) sr-only rounded-sm text-sm focus:not-sr-only focus-visible:ring-2 focus-visible:outline-none"
							data-testid="suites-tasks-skip"
						>
							{tf("tasks.skipTable")}
						</a>
						<SuiteTaskTable
							suiteId={suite.id}
							rows={paged.rows}
							sort={sort}
							onSort={onSort}
							selected={openTask}
							onOpen={(id) => void setQ({ task: id === openTask ? null : id })}
							health={health}
							moreColumns={moreColumns}
							categories={categories}
						/>
						<div id="suites-task-pager">
							<Pager
								page={paged.page}
								pageCount={paged.pages}
								onPage={(page) => void setQ({ page: page === 1 ? null : page })}
								rangeText={
									paged.total < suite.tasks.length
										? tf("tasks.pageOfFiltered", { first: paged.first, last: paged.last, total: paged.total, all: suite.tasks.length })
										: tf("tasks.pageOf", { first: paged.first, last: paged.last, total: paged.total })
								}
								testId="suites-tasks"
							/>
						</div>
					</>
				)}
			</section>
		</div>
	);
}
