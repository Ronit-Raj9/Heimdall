import { Section } from "@/components/runs/screenStates";
import type { SuiteAdmissionCheck, SuiteSummary, SuiteTask } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CheckCircle2, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import "./w444I18n";

/** The eight quality checks of a generated task, in the order admission records them. */
export const GUARD_CHECKS = [
	"guard_noop_fails",
	"guard_control_fails",
	"guard_no_answer_leak",
	"guard_not_flaky",
	"guard_deterministic",
	"guard_instruction_clear",
	"guard_provenance",
	"guard_privacy",
] as const;

type GuardCheckName = (typeof GUARD_CHECKS)[number];

const isGuardCheck = (name: string): name is GuardCheckName => (GUARD_CHECKS as readonly string[]).includes(name);

/** The guard checks of a task's last admission; empty for a hand-written task (it is never guarded). */
export function guardChecksOf(task: SuiteTask): SuiteAdmissionCheck[] {
	return (task.checks ?? []).filter((c) => isGuardCheck(c.name));
}

/** The tasks of a suite whose last admission ran the quality checks. */
export function guardedTasks(suite: SuiteSummary): SuiteTask[] {
	return (suite.tasks ?? []).filter((t) => guardChecksOf(t).length > 0);
}

function TaskGuard({ task }: { task: SuiteTask }) {
	const { t } = useTranslation("w444");
	const checks = guardChecksOf(task);
	const failed = checks.filter((c) => !c.passed).length;
	return (
		<li className="flex min-w-0 flex-col gap-2 rounded-md border p-3" data-testid={`guard-task-${task.id}`}>
			<span className="truncate text-sm font-medium">{task.title || task.id}</span>
			{failed > 0 && (
				<p className="text-destructive text-sm" data-testid={`guard-not-added-${task.id}`}>
					{t("guard.notAdded", { count: failed })}
				</p>
			)}
			<ul className="flex flex-col gap-1">
				{checks.map((c) => (
					<li key={c.name} className="flex items-start gap-2 text-sm" data-testid={`guard-check-${task.id}-${c.name}`}>
						{c.passed ? (
							<CheckCircle2 className="mt-0.5 size-4 shrink-0 text-green-600" aria-hidden="true" />
						) : (
							<XCircle className="text-destructive mt-0.5 size-4 shrink-0" aria-hidden="true" />
						)}
						<span className="sr-only">{c.passed ? t("guard.passed") : t("guard.failed")}</span>
						<span className="flex min-w-0 flex-col">
							<span className={cn(!c.passed && "font-medium")}>{t(`guard.checks.${c.name as GuardCheckName}`)}</span>
							{!c.passed && c.summary && <span className="text-muted-foreground text-xs">{c.summary}</span>}
						</span>
					</li>
				))}
			</ul>
		</li>
	);
}

/** Readiness view: the quality checks of every generated task of the suite, with a green or red mark. Hidden when no task was guarded. */
export function GuardChecks({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("w444");
	const tasks = guardedTasks(suite);
	if (tasks.length === 0) return null;
	return (
		<Section title={t("guard.heading")} testId="suite-review-guard">
			<ul className="flex min-w-0 flex-col gap-3">
				{tasks.map((task) => (
					<TaskGuard key={task.id} task={task} />
				))}
			</ul>
		</Section>
	);
}