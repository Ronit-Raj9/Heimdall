import { EmptyNote, Section } from "@/components/runs/screenStates";
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
import { TaskDetailPanel } from "@/components/suites/taskDetailPanel";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
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
};

/** Tasks tab: the filterable task table, the task in ?task= open beside it. */
export function SuiteTasksTab({ suite, health }: { suite: SuiteSummary; health: HealthState }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("suitesUx");
	const { t: tg } = useTranslation("gw13");
	const [q, setQ] = useQueryStates(TASK_QUERY);
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
				filterTasks(suite.tasks, {
					kind: q.kind,
					difficulty: q.difficulty,
					capability: q.cap,
					status: q.status,
				}),
				parseTaskSort(q.sort, q.dir),
			),
		[suite.tasks, q.kind, q.difficulty, q.cap, q.status, q.sort, q.dir],
	);
	// Expert columns (kind, skills tested, license, hidden checks) stay behind a toggle.
	const [moreColumns, setMoreColumns] = useState(false);
	const openTask = q.task && suite.tasks.some((task) => task.id === q.task) ? q.task : null;
	// A ?task= the benchmark does not have (removed in this version) is named, not dropped silently.
	const missingTask = q.task && !openTask ? q.task : null;
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
					<TaskDetailPanel
						suiteId={suite.id}
						suiteVersion={suite.version}
						taskId={openTask}
						suiteOutput={suite.output}
						suiteArtifacts={suite.artifacts}
						onClose={() => void setQ({ task: null })}
						health={health}
						quarantine={suite.tasks.find((task) => task.id === openTask)?.quarantine ?? null}
					/>
				</div>
			)}
			<Section title={tu("tasksTitle")} description={tu("tasksDescription")} testId="suites-tasks" className="min-w-0">
				<SuiteTaskFilters
					filters={filters}
					options={options}
					onChange={(next) =>
						void setQ({
							kind: next.kind,
							difficulty: next.difficulty,
							cap: next.capability,
							status: next.status,
						})
					}
					onReset={() => void setQ({ kind: null, difficulty: null, cap: null, status: null })}
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
						<div className="flex justify-end">
							<Button
								size="sm"
								variant="ghost"
								aria-pressed={moreColumns}
								onClick={() => setMoreColumns((v) => !v)}
								data-testid="suites-tasks-more-columns"
							>
								{moreColumns ? tu("fewerColumns") : tu("moreColumns")}
							</Button>
						</div>
						<SuiteTaskTable
							suiteId={suite.id}
							rows={rows}
							sort={sort}
							onSort={onSort}
							selected={openTask}
							onOpen={(id) => void setQ({ task: id === openTask ? null : id })}
							health={health}
							moreColumns={moreColumns}
						/>
						{rows.length < suite.tasks.length && (
							<p className="text-muted-foreground text-xs" data-testid="suites-tasks-showing">
								{t("suites.tasks.showing", {
									shown: rows.length,
									total: suite.tasks.length,
								})}
							</p>
						)}
					</>
				)}
			</Section>
		</div>
	);
}