import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { SuiteTask } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AdmissionBadge } from "./suiteBadges";
import { HeldOutBadge, TaskHealthCell } from "./suiteHealth";
import { isHeldOut, isRedacted, type HealthState } from "./suiteHealth.utils";
import { taskLicense, type TaskSort, type TaskSortKey } from "./suites.utils";
import { TaskCategoryCell, type TaskCategories } from "./taskCategoryCell";
import "./uf6aI18n";

export interface SuiteTaskTableProps {
	suiteId: string;
	/** Tasks in the order to show them. */
	rows: readonly SuiteTask[];
	sort: TaskSort;
	onSort: (key: TaskSortKey) => void;
	/** The task whose detail is open, if any. */
	selected: string | null;
	onOpen: (taskId: string) => void;
	/** Suite health; when given, the table gets a health column. */
	health?: HealthState;
	/** Also show the expert columns: kind, skills tested, license and hidden checks. Off by default. */
	moreColumns?: boolean;
	/** The kind of work of each task (W4-44); when given, the expert columns gain a "Kind of work" column. */
	categories?: TaskCategories;
}

/**
 * One row per task. Each sortable header is a button with aria-sort; the task id opens its detail.
 * A held-out task the server withholds shows its id and a "held out" badge only, with no detail link.
 */
export function SuiteTaskTable({
	suiteId,
	rows,
	sort,
	onSort,
	selected,
	onOpen,
	health,
	moreColumns = false,
	categories,
}: SuiteTaskTableProps) {
	const { t } = useTranslation("bench");
	const { t: tc } = useTranslation("w444");
	const { t: tf } = useTranslation("uf6a");
	const kindOfWork = moreColumns && categories !== undefined;
	const head = (key: TaskSortKey, label: string, align: "left" | "right" = "left", className?: string) => (
		<SortableHead sortKey={key} label={label} sort={sort} onSort={onSort} align={align} className={className} />
	);
	// M6-3: on phones only Task and Status stay; difficulty and health come back from sm up.
	const wideOnly = "hidden sm:table-cell";
	return (
		<Table data-testid="suites-task-table" aria-label={t("suites.tasks.label", { suite: suiteId })} containerClassName="rounded-xl border">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					{head("id", t("suites.tasks.task"))}
					{moreColumns && head("kind", t("suites.tasks.kind"))}
					{kindOfWork && <TableHead data-testid="suites-head-category">{tc("categories.column")}</TableHead>}
					{head("difficulty", t("suites.tasks.difficulty"), "left", wideOnly)}
					{moreColumns && <TableHead>{t("suites.tasks.capabilities")}</TableHead>}
					{head("admission", tf("tasks.statusColumn"))}
					{moreColumns && head("license", t("suites.tasks.license"))}
					{moreColumns && head("hidden", t("suites.tasks.hiddenTests"), "right")}
					{health && <TableHead className={wideOnly} data-testid="suites-head-health">{t("suiteHealth.column")}</TableHead>}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((task) => {
					if (isRedacted(task))
						return <RedactedRow key={task.id} task={task} health={health} moreColumns={moreColumns} kindOfWork={kindOfWork} />;
					const license = taskLicense(task);
					return (
						<TableRow
							key={task.id}
							data-testid={`suites-task-row-${task.id}`}
							data-task-id={task.id}
							data-state={selected === task.id ? "selected" : undefined}
							className={cn(selected === task.id && "bg-muted/60")}
						>
							<TableCell className="max-w-[14rem] align-top break-words whitespace-normal sm:max-w-80 sm:min-w-48">
								<button
									type="button"
									onClick={() => onOpen(task.id)}
									aria-expanded={selected === task.id}
									aria-label={t("suites.tasks.open", {
										task: `${task.id}: ${task.title}`,
									})}
									className="focus-visible:ring-(--focus) group flex cursor-pointer flex-col items-start gap-0.5 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
									data-testid={`suites-task-open-${task.id}`}
								>
									<span className="text-foreground text-sm font-medium break-words group-hover:underline">{task.title || task.id}</span>
									{/* M6-4: the id prints once when the title is the id. */}
									{task.title && task.title !== task.id && (
										<span className="text-muted-foreground font-mono text-xs break-all">{task.id}</span>
									)}
								</button>
								{isHeldOut(task) && <HeldOutBadge task={task} testId={`suites-task-held-out-${task.id}`} className="mt-1" />}
							</TableCell>
							{moreColumns && <TableCell className="align-top font-mono text-xs">{task.kind}</TableCell>}
							{kindOfWork && (
								<TableCell className="align-top text-xs">
									<TaskCategoryCell suiteId={suiteId} taskId={task.id} label={categories.labels?.[task.id]} canEdit={categories.canEdit} />
								</TableCell>
							)}
							<TableCell className={cn("align-top text-xs", wideOnly)}>
								{/* M6-6: difficulty is plain grey text; only the status is a pill. */}
								{task.difficulty ? (
									<span className="text-muted-foreground text-xs">
										{task.difficulty === "easy" || task.difficulty === "medium" || task.difficulty === "hard"
											? t(`suites.difficulty.${task.difficulty}`)
											: task.difficulty}
									</span>
								) : (
									<span className="text-muted-foreground" aria-label={t("suites.tasks.unset")}>
										-
									</span>
								)}
							</TableCell>
							{moreColumns && (
								<TableCell className="max-w-64 align-top whitespace-normal">
									{task.capabilities.length > 0 ? (
										<span className="flex flex-wrap gap-1">
											{task.capabilities.map((capability) => (
												<span key={capability} className="bg-muted text-muted-foreground rounded-sm px-1.5 py-0.5 font-mono text-[11px]">
													{capability}
												</span>
											))}
										</span>
									) : (
										<span className="text-muted-foreground text-xs">{t("suites.tasks.unset")}</span>
									)}
								</TableCell>
							)}
							<TableCell className="align-top">
								{/* M6-2: the badge says "Not checked yet" itself; the count line shows only once a check ran. */}
								<span className="flex flex-col items-start gap-1">
									<AdmissionBadge task={task} testId={`suites-task-admission-${task.id}`} />
									{task.checks.length > 0 && (
										<span className="text-muted-foreground text-xs tabular-nums">
											{t("suites.tasks.checksPassed", {
												passed: task.checks.filter((check) => check.passed).length,
												total: task.checks.length,
											})}
										</span>
									)}
								</span>
							</TableCell>
							{moreColumns && (
								<TableCell className="align-top text-xs" data-testid={`suites-task-license-${task.id}`}>
									{license ? (
										<span className="font-mono">{license}</span>
									) : (
										<span className="text-warning-ink">{t("suites.tasks.noLicense")}</span>
									)}
								</TableCell>
							)}
							{moreColumns && (
								<TableCell className="text-muted-foreground text-right align-top text-xs tabular-nums">
									{t("suites.tasks.hiddenTestsCell", {
										files: task.hidden_test_files,
										required: task.required_tests,
									})}
								</TableCell>
							)}
							{health && (
								<TableCell className={cn("max-w-72 align-top whitespace-normal", wideOnly)}>
									<TaskHealthCell task={task} health={health} />
								</TableCell>
							)}
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}

/**
 * A held-out task the server serves by id only: its id and a "held out" badge, no link to a detail
 * the server would refuse, its admission state, and one note where the withheld columns would be.
 */
function RedactedRow({
	task,
	health,
	moreColumns,
	kindOfWork,
}: {
	task: SuiteTask;
	health?: HealthState;
	moreColumns: boolean;
	kindOfWork: boolean;
}) {
	const { t } = useTranslation("bench");
	return (
		<TableRow data-testid={`suites-task-row-${task.id}`} data-redacted="true">
			<TableCell className="max-w-[14rem] align-top break-words whitespace-normal sm:max-w-80 sm:min-w-48">
				<span className="flex flex-col items-start gap-1">
					<span className="text-foreground font-mono text-xs">{task.id}</span>
					<HeldOutBadge task={task} testId={`suites-task-held-out-${task.id}`} />
				</span>
			</TableCell>
			<TableCell
				colSpan={moreColumns ? (kindOfWork ? 4 : 3) : 1}
				className={cn("text-muted-foreground align-top text-xs whitespace-normal", !moreColumns && "hidden sm:table-cell")}
			>
				{t("suiteHealth.heldOutRow")}
			</TableCell>
			<TableCell className="align-top">
				<AdmissionBadge task={task} testId={`suites-task-admission-${task.id}`} />
			</TableCell>
			{moreColumns && (
				<TableCell colSpan={2} className="text-muted-foreground align-top text-xs">
					{t("suiteHealth.withheld")}
				</TableCell>
			)}
			{health && (
				<TableCell className="hidden max-w-72 align-top whitespace-normal sm:table-cell">
					<TaskHealthCell task={task} health={health} />
				</TableCell>
			)}
		</TableRow>
	);
}

interface SortableHeadProps {
	sortKey: TaskSortKey;
	label: string;
	sort: TaskSort;
	onSort: (key: TaskSortKey) => void;
	align: "left" | "right";
	className?: string;
}

/** A header that sorts its column: a real button, so it is reachable and operable by keyboard. */
function SortableHead({ sortKey, label, sort, onSort, align, className }: SortableHeadProps) {
	const { t } = useTranslation("bench");
	const active = sort.key === sortKey;
	const Icon = !active ? ArrowUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
	const name = t("suites.tasks.sortBy", { column: label });
	return (
		<TableHead
			aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
			className={cn(align === "right" && "text-right", className)}
			data-testid={`suites-head-${sortKey}`}
		>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				aria-label={active ? `${name}, ${t(sort.dir === "asc" ? "suites.tasks.sortedAsc" : "suites.tasks.sortedDesc")}` : name}
				className={cn(
					"focus-visible:ring-(--focus) -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none",
					active ? "text-foreground" : "text-muted-foreground hover:text-foreground",
					align === "right" && "flex-row-reverse",
				)}
				data-testid={`suites-sort-${sortKey}`}
			>
				<span>{label}</span>
				<Icon className={cn("size-3.5 shrink-0", !active && "opacity-50")} aria-hidden="true" />
			</button>
		</TableHead>
	);
}