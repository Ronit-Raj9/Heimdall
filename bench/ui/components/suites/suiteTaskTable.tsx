import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { SuiteTask } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AdmissionBadge } from "./suiteBadges";
import { taskLicense, type TaskSort, type TaskSortKey } from "./suites.utils";

export interface SuiteTaskTableProps {
	suiteId: string;
	/** Tasks in the order to show them. */
	rows: readonly SuiteTask[];
	sort: TaskSort;
	onSort: (key: TaskSortKey) => void;
	/** The task whose detail is open, if any. */
	selected: string | null;
	onOpen: (taskId: string) => void;
}

/** One row per task. Each sortable header is a button with aria-sort; the task id opens its detail. */
export function SuiteTaskTable({ suiteId, rows, sort, onSort, selected, onOpen }: SuiteTaskTableProps) {
	const { t } = useTranslation("bench");
	const head = (key: TaskSortKey, label: string, align: "left" | "right" = "left") => (
		<SortableHead sortKey={key} label={label} sort={sort} onSort={onSort} align={align} />
	);
	return (
		<Table data-testid="suites-task-table" aria-label={t("suites.tasks.label", { suite: suiteId })} containerClassName="rounded-sm border">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					{head("id", t("suites.tasks.task"))}
					{head("kind", t("suites.tasks.kind"))}
					{head("difficulty", t("suites.tasks.difficulty"))}
					<TableHead>{t("suites.tasks.capabilities")}</TableHead>
					{head("admission", t("suites.tasks.admission"))}
					{head("license", t("suites.tasks.license"))}
					{head("hidden", t("suites.tasks.hiddenTests"), "right")}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((task) => {
					const license = taskLicense(task);
					const passed = task.checks.filter((check) => check.passed).length;
					return (
						<TableRow
							key={task.id}
							data-testid={`suites-task-row-${task.id}`}
							data-state={selected === task.id ? "selected" : undefined}
							className={cn(selected === task.id && "bg-muted/60")}
						>
							<TableCell className="max-w-80 min-w-48 align-top whitespace-normal">
								<button
									type="button"
									onClick={() => onOpen(task.id)}
									aria-expanded={selected === task.id}
									aria-label={t("suites.tasks.open", { task: `${task.id}: ${task.title}` })}
									className="focus-visible:ring-ring/50 group flex cursor-pointer flex-col items-start gap-0.5 rounded-sm text-left focus-visible:ring-[3px] focus-visible:outline-none"
									data-testid={`suites-task-open-${task.id}`}
								>
									<span className="text-primary font-mono text-xs group-hover:underline">{task.id}</span>
									<span className="text-foreground text-sm">{task.title}</span>
								</button>
							</TableCell>
							<TableCell className="align-top font-mono text-xs">{task.kind}</TableCell>
							<TableCell className="align-top text-xs">
								{task.difficulty ? (
									task.difficulty === "easy" || task.difficulty === "medium" || task.difficulty === "hard" ? (
										t(`suites.difficulty.${task.difficulty}`)
									) : (
										task.difficulty
									)
								) : (
									<span className="text-muted-foreground">{t("suites.tasks.unset")}</span>
								)}
							</TableCell>
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
							<TableCell className="align-top">
								<span className="flex flex-col items-start gap-1">
									<AdmissionBadge task={task} testId={`suites-task-admission-${task.id}`} />
									<span className="text-muted-foreground text-[11px] tabular-nums">
										{task.checks.length > 0
											? t("suites.tasks.checksPassed", { passed, total: task.checks.length })
											: t("suites.tasks.noChecks")}
									</span>
								</span>
							</TableCell>
							<TableCell className="align-top text-xs" data-testid={`suites-task-license-${task.id}`}>
								{license ? (
									<span className="font-mono">{license}</span>
								) : (
									<span className="text-chart-warning-ink">{t("suites.tasks.noLicense")}</span>
								)}
							</TableCell>
							<TableCell className="text-muted-foreground text-right align-top text-xs tabular-nums">
								{t("suites.tasks.hiddenTestsCell", { files: task.hidden_test_files, required: task.required_tests })}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}

interface SortableHeadProps {
	sortKey: TaskSortKey;
	label: string;
	sort: TaskSort;
	onSort: (key: TaskSortKey) => void;
	align: "left" | "right";
}

/** A header that sorts its column: a real button, so it is reachable and operable by keyboard. */
function SortableHead({ sortKey, label, sort, onSort, align }: SortableHeadProps) {
	const { t } = useTranslation("bench");
	const active = sort.key === sortKey;
	const Icon = !active ? ArrowUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
	const name = t("suites.tasks.sortBy", { column: label });
	return (
		<TableHead
			aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
			className={cn(align === "right" && "text-right")}
			data-testid={`suites-head-${sortKey}`}
		>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				aria-label={active ? `${name}, ${t(sort.dir === "asc" ? "suites.tasks.sortedAsc" : "suites.tasks.sortedDesc")}` : name}
				className={cn(
					"focus-visible:ring-ring/50 -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
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