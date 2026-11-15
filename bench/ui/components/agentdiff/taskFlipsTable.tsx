import { EmptyNote } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { AgentDiffOutcome, AgentDiffTask } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CLASS_TONE, formatRate, isChangedClass, runLinks, testIdPart, visibleTasks } from "./agentDiff.utils";

const linkClass = "text-primary text-xs underline-offset-4 hover:underline";

/**
 * Per-task pass counts of A and B. Flips and moves show by default, each with a link to the run
 * comparison view; a toggle shows every common task. Tasks only one version ran are listed below,
 * since they are left out of the comparison.
 */
export function TaskFlipsTable({ outcome }: { outcome: AgentDiffOutcome }) {
	const { t } = useTranslation("bench");
	const [showAll, setShowAll] = useState(false);
	const tasks = outcome.tasks ?? [];
	const changed = tasks.filter((task) => isChangedClass(task.class)).length;
	const rows = visibleTasks(tasks, showAll);
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="agentdiff-tasks">
			{tasks.length === 0 ? (
				<EmptyNote testId="agentdiff-tasks-empty">{t("agentDiff.tasks.noCommon")}</EmptyNote>
			) : (
				<>
					{rows.length === 0 ? (
						<EmptyNote testId="agentdiff-tasks-none-changed">{t("agentDiff.tasks.noneChanged")}</EmptyNote>
					) : (
						<div className="min-w-0 rounded-sm border">
							<Table
								stickyColumns={1}
								data-testid="agentdiff-tasks-table"
								aria-label={t("agentDiff.tasks.title")}
								containerClassName="rounded-sm"
							>
								<TableHeader>
									<TableRow className="hover:bg-transparent">
										<TableHead>{t("agentDiff.tasks.task")}</TableHead>
										<TableHead>{t("agentDiff.tasks.class")}</TableHead>
										<TableHead className="text-right">{t("agentDiff.tasks.a")}</TableHead>
										<TableHead className="text-right">{t("agentDiff.tasks.b")}</TableHead>
										<TableHead>{t("agentDiff.tasks.runs")}</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{rows.map((task) => (
										<TaskRow key={task.task} task={task} />
									))}
								</TableBody>
							</Table>
						</div>
					)}
					{changed < tasks.length && (
						<Button
							variant="outline"
							size="sm"
							className="self-start"
							aria-pressed={showAll}
							onClick={() => setShowAll((on) => !on)}
							dataTestId="agentdiff-tasks-toggle"
						>
							{showAll ? t("agentDiff.tasks.showChanged") : t("agentDiff.tasks.showAll", { count: tasks.length })}
						</Button>
					)}
				</>
			)}
			<OnlyOneSide side="a" tasks={outcome.only_a ?? []} />
			<OnlyOneSide side="b" tasks={outcome.only_b ?? []} />
		</div>
	);
}

function TaskRow({ task }: { task: AgentDiffTask }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const id = testIdPart(task.task);
	return (
		<TableRow data-testid={`agentdiff-task-${id}`} data-class={task.class}>
			<TableCell className="font-mono text-xs">{task.task}</TableCell>
			<TableCell>
				<ToneBadge tone={CLASS_TONE[task.class] ?? "neutral"} hint={t(`agentDiff.classHint.${task.class}`)}>
					{t(`agentDiff.class.${task.class}`, { defaultValue: task.class })}
				</ToneBadge>
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{t("agentDiff.tasks.passOf", { pass: task.pass_a, n: task.n_a })}
				<span className="text-muted-foreground ml-1.5">{formatRate(task.rate_a, locale)}</span>
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{t("agentDiff.tasks.passOf", { pass: task.pass_b, n: task.n_b })}
				<span className="text-muted-foreground ml-1.5">{formatRate(task.rate_b, locale)}</span>
			</TableCell>
			<TableCell>
				<RunLinksCell task={task} />
			</TableCell>
		</TableRow>
	);
}

function RunLinksCell({ task }: { task: AgentDiffTask }) {
	const { t } = useTranslation("bench");
	const links = runLinks(task);
	const id = testIdPart(task.task);
	if (!links) return <span className="text-muted-foreground text-xs">{t("agentDiff.tasks.noRuns")}</span>;
	if (links.kind === "pair") {
		return (
			<Link
				to="/runs"
				search={{ pair: links.pairId }}
				className={linkClass}
				aria-label={t("agentDiff.tasks.openPairAria", { task: task.task })}
				data-testid={`agentdiff-task-${id}-pair`}
			>
				{t("agentDiff.tasks.openPair")}
			</Link>
		);
	}
	return (
		<span className="flex flex-wrap gap-2">
			{links.a && (
				<Link
					to="/runs"
					search={{ pair: links.a }}
					className={linkClass}
					aria-label={t("agentDiff.tasks.openAAria", { task: task.task })}
					data-testid={`agentdiff-task-${id}-a`}
				>
					{t("agentDiff.tasks.openA")}
				</Link>
			)}
			{links.b && (
				<Link
					to="/runs"
					search={{ pair: links.b }}
					className={linkClass}
					aria-label={t("agentDiff.tasks.openBAria", { task: task.task })}
					data-testid={`agentdiff-task-${id}-b`}
				>
					{t("agentDiff.tasks.openB")}
				</Link>
			)}
		</span>
	);
}

function OnlyOneSide({ side, tasks }: { side: "a" | "b"; tasks: string[] }) {
	const { t } = useTranslation("bench");
	if (tasks.length === 0) return null;
	return (
		<p className="text-muted-foreground text-xs" role="note" data-testid={`agentdiff-only-${side}`}>
			{t(side === "a" ? "agentDiff.tasks.onlyA" : "agentDiff.tasks.onlyB", { count: tasks.length, list: tasks.join(", ") })}
		</p>
	);
}