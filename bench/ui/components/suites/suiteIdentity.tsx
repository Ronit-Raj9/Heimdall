import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./gw12I18n";

// Lane U04, R02: the suite's readiness, as a small dot plus a few words. Names live in suiteDisplay.

export { suiteDisplayName } from "./suiteDisplay";

export type SuiteReadiness = "ready" | "needsAdmission" | "notReady" | "needsLock" | "noTasks" | "error";

/** One status for the whole suite: ready means every task admitted and the lock current. */
export function suiteReadiness(suite: Pick<SuiteSummary, "error" | "task_count" | "admitted_count" | "lock_state">): SuiteReadiness {
	if (suite.error) return "error";
	if (suite.task_count === 0) return "noTasks";
	// Tasks listed, none admitted: nothing can run yet (packs and the wizard leave it out).
	if ((suite.admitted_count ?? 0) === 0) return "notReady";
	if (suite.admitted_count < suite.task_count) return "needsAdmission";
	return suite.lock_state === "locked" ? "ready" : "needsLock";
}

/** The header pill's state: suiteReadiness, plus "frozenNotChecked" when the lock is current and no task is admitted yet. */
export type SuitePillState = SuiteReadiness | "frozenNotChecked";

/** After Freeze, before the readiness check: says the step just done instead of a bare "Not ready yet" (N116). */
export function suitePillState(suite: Pick<SuiteSummary, "error" | "task_count" | "admitted_count" | "lock_state">): SuitePillState {
	const state = suiteReadiness(suite);
	return state === "notReady" && suite.lock_state === "locked" ? "frozenNotChecked" : state;
}

const DOT_TONE: Record<SuitePillState, string> = {
	ready: "bg-chart-success",
	needsAdmission: "bg-chart-warning",
	notReady: "bg-muted-foreground/60",
	frozenNotChecked: "bg-muted-foreground/60",
	needsLock: "bg-chart-warning",
	noTasks: "bg-muted-foreground/60",
	error: "bg-chart-error",
};

/** The suite's readiness: a small colored dot and a few words ("Ready", "2 of 3 tasks ready"). */
export function SuiteStatusPill({ suite, testId, className }: { suite: SuiteSummary; testId?: string; className?: string }) {
	const { t } = useTranslation("suitesUx");
	const { t: tg } = useTranslation("gw12");
	const state = suitePillState(suite);
	return (
		<span
			className={cn("text-muted-foreground inline-flex w-fit items-center gap-1.5 text-xs whitespace-nowrap", className)}
			data-testid={testId}
			data-status={state}
		>
			<span className={cn("size-1.5 shrink-0 rounded-full", DOT_TONE[state])} aria-hidden="true" />
			{state === "frozenNotChecked" ? tg("status.frozenNotChecked") : t(`status.${state}`, { ready: suite.admitted_count, total: suite.task_count })}
		</span>
	);
}
