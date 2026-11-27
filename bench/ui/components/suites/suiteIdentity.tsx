import type { SuiteSummary } from "@/lib/types/bench";
import { StatusPill, type StatusTone } from "@/components/ui/statusPill";
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

/** M6-45: the shared pill tones. Partly ready is normal (a built-in benchmark admits a subset by design): neutral, not amber. */
export const PILL_TONE: Record<SuitePillState, StatusTone> = {
	ready: "ok",
	needsAdmission: "neutral",
	notReady: "neutral",
	frozenNotChecked: "neutral",
	needsLock: "warn",
	noTasks: "neutral",
	error: "bad",
};

/** The suite's readiness: the shared StatusPill with a few words ("Ready", "2 of 3 tasks ready"). */
export function SuiteStatusPill({ suite, testId, className }: { suite: SuiteSummary; testId?: string; className?: string }) {
	const { t } = useTranslation("suitesUx");
	const { t: tg } = useTranslation("gw12");
	const state = suitePillState(suite);
	return (
		<StatusPill tone={PILL_TONE[state]} className={cn("w-fit whitespace-nowrap", className)} testId={testId} data-status={state}>
			{state === "frozenNotChecked"
				? tg("status.frozenNotChecked")
				: t(`status.${state}`, { ready: suite.admitted_count, total: suite.task_count })}
		</StatusPill>
	);
}
