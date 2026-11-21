import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

// Lane U04, R02: the suite's readiness, as a small dot plus a few words. Names live in suiteDisplay.

export { suiteDisplayName } from "./suiteDisplay";

export type SuiteReadiness = "ready" | "needsAdmission" | "needsLock" | "noTasks" | "error";

/** One status for the whole suite: ready means every task admitted and the lock current. */
export function suiteReadiness(suite: Pick<SuiteSummary, "error" | "task_count" | "admitted_count" | "lock_state">): SuiteReadiness {
	if (suite.error) return "error";
	if (suite.task_count === 0) return "noTasks";
	if (suite.admitted_count < suite.task_count) return "needsAdmission";
	return suite.lock_state === "locked" ? "ready" : "needsLock";
}

const DOT_TONE: Record<SuiteReadiness, string> = {
	ready: "bg-chart-success",
	needsAdmission: "bg-chart-warning",
	needsLock: "bg-chart-warning",
	noTasks: "bg-muted-foreground/60",
	error: "bg-chart-error",
};

/** The suite's readiness: a small colored dot and a few words ("Ready", "2 of 3 tasks ready"). */
export function SuiteStatusPill({ suite, testId, className }: { suite: SuiteSummary; testId?: string; className?: string }) {
	const { t } = useTranslation("suitesUx");
	const state = suiteReadiness(suite);
	return (
		<span
			className={cn("text-muted-foreground inline-flex w-fit items-center gap-1.5 text-xs whitespace-nowrap", className)}
			data-testid={testId}
			data-status={state}
		>
			<span className={cn("size-1.5 shrink-0 rounded-full", DOT_TONE[state])} aria-hidden="true" />
			{t(`status.${state}`, { ready: suite.admitted_count, total: suite.task_count })}
		</span>
	);
}
