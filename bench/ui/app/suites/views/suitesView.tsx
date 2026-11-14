import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { AddYourOwnPanel } from "@/components/suites/addYourOwnPanel";
import { SuiteCards } from "@/components/suites/suiteCards";
import { CapabilityCoverage, LicenseSummary, ProbeList } from "@/components/suites/suiteCoverage";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "@/components/suites/suiteFacts";
import { SuiteTaskFilters } from "@/components/suites/suiteTaskFilters";
import { SuiteTaskTable } from "@/components/suites/suiteTaskTable";
import {
	ADMISSION_STATES,
	DEFAULT_TASK_SORT,
	SORT_DIRS,
	defaultSuiteId,
	filterOptions,
	filterTasks,
	nextTaskSort,
	parseTaskSort,
	sortTasks,
	type TaskFilters,
	type TaskSortKey,
} from "@/components/suites/suites.utils";
import { TaskDetailPanel } from "@/components/suites/taskDetailPanel";
import { useGetSuitesQuery } from "@/lib/store";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryState, useQueryStates } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/** URL state of the task table: filters, sort and the open task, all per suite. */
const TASK_QUERY = {
	kind: parseAsString,
	difficulty: parseAsString,
	cap: parseAsString,
	status: parseAsStringLiteral(ADMISSION_STATES),
	sort: parseAsString,
	dir: parseAsStringLiteral(SORT_DIRS),
	task: parseAsString,
};

/**
 * The suites screen: /suites?suite=&kind=&difficulty=&cap=&status=&sort=&dir=&task=. Every pack in
 * the project with its lock state and admission record, and how to add your own.
 */
export default function SuitesView() {
	const { t } = useTranslation("bench");
	const query = useGetSuitesQuery();
	const [suiteParam, setSuiteParam] = useQueryState("suite", parseAsString);
	const [, setTaskQuery] = useQueryStates(TASK_QUERY);
	const data = query.data;
	const suites = useMemo(() => data?.suites ?? [], [data]);
	const selectedId = suites.some((s) => s.id === suiteParam) ? suiteParam : defaultSuiteId(suites);
	const selected = suites.find((s) => s.id === selectedId) ?? null;

	const selectSuite = (id: string) => {
		// Filters, sort and the open task belong to the suite they were set on.
		void setSuiteParam(id === defaultSuiteId(suites) ? null : id);
		void setTaskQuery(null);
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="suites-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("suites.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm" data-testid="suites-explainer">
					{t("suites.subtitle")}
				</p>
			</header>

			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="suites-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.synthetic")}
				</p>
			)}

			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="suites-loading" />
			) : query.isError || !data ? (
				<LoadError title={t("suites.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="suites-error" />
			) : suites.length === 0 ? (
				<EmptyNote testId="suites-empty">{t("suites.empty")}</EmptyNote>
			) : (
				<div
					className={cn("flex min-w-0 flex-col gap-6 transition-opacity", query.isFetching && "opacity-60")}
					aria-busy={query.isFetching}
				>
					<SuiteCards suites={suites} selected={selectedId} onSelect={selectSuite} />
					{selected && <SuiteDetail key={selected.id} suite={selected} />}
				</div>
			)}

			<AddYourOwnPanel />
		</section>
	);
}

/** One suite: its facts, drift, task table with the open task, coverage, licenses and probes. */
function SuiteDetail({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const [q, setQ] = useQueryStates(TASK_QUERY);
	const filters: TaskFilters = { kind: q.kind, difficulty: q.difficulty, capability: q.cap, status: q.status };
	const sort = parseTaskSort(q.sort, q.dir);
	const options = useMemo(() => filterOptions(suite.tasks), [suite.tasks]);
	const rows = useMemo(
		() =>
			sortTasks(
				filterTasks(suite.tasks, { kind: q.kind, difficulty: q.difficulty, capability: q.cap, status: q.status }),
				parseTaskSort(q.sort, q.dir),
			),
		[suite.tasks, q.kind, q.difficulty, q.cap, q.status, q.sort, q.dir],
	);
	const openTask = q.task && suite.tasks.some((task) => task.id === q.task) ? q.task : null;

	if (suite.error) return <BrokenSuite suite={suite} />;

	const onSort = (key: TaskSortKey) => {
		const next = nextTaskSort(sort, key);
		const isDefault = next.key === DEFAULT_TASK_SORT.key && next.dir === DEFAULT_TASK_SORT.dir;
		void setQ({ sort: isDefault ? null : next.key, dir: isDefault ? null : next.dir });
	};

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid={`suites-detail-${suite.id}`}>
			<SuiteFacts suite={suite} />
			<SuiteDriftNotice suite={suite} />
			<div className={cn("grid min-w-0 grid-cols-1 gap-4", openTask && "2xl:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]")}>
				<Section title={t("suites.tasks.title")} description={t("suites.tasks.description")} testId="suites-tasks" className="min-w-0">
					<SuiteTaskFilters
						filters={filters}
						options={options}
						onChange={(next) => void setQ({ kind: next.kind, difficulty: next.difficulty, cap: next.capability, status: next.status })}
						onReset={() => void setQ({ kind: null, difficulty: null, cap: null, status: null })}
					/>
					{suite.tasks.length === 0 ? (
						<EmptyNote testId="suites-tasks-empty">{t("suites.tasks.empty")}</EmptyNote>
					) : rows.length === 0 ? (
						<EmptyNote testId="suites-tasks-no-match">{t("suites.tasks.noMatch")}</EmptyNote>
					) : (
						<>
							<SuiteTaskTable
								suiteId={suite.id}
								rows={rows}
								sort={sort}
								onSort={onSort}
								selected={openTask}
								onOpen={(id) => void setQ({ task: id === openTask ? null : id })}
							/>
							{rows.length < suite.tasks.length && (
								<p className="text-muted-foreground text-xs" data-testid="suites-tasks-showing">
									{t("suites.tasks.showing", { shown: rows.length, total: suite.tasks.length })}
								</p>
							)}
						</>
					)}
				</Section>
				{openTask && (
					<div className="min-w-0 2xl:sticky 2xl:top-4 2xl:self-start">
						<TaskDetailPanel suiteId={suite.id} suiteVersion={suite.version} taskId={openTask} onClose={() => void setQ({ task: null })} />
					</div>
				)}
			</div>
			<div className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2">
				<CapabilityCoverage tasks={suite.tasks} />
				<LicenseSummary tasks={suite.tasks} />
			</div>
			<ProbeList probes={suite.probes} />
		</div>
	);
}