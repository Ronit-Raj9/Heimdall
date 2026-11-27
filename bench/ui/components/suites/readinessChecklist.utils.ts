import type { SuiteSummary } from "@/lib/types/bench";

// M6-12: the three steps that make a benchmark ready, with the state of each, derived from the
// suite alone (lock state and ready count). Pure, so the states are testable without a browser.

export type ChecklistStepKey = "lint" | "freeze" | "admit";

/** todo: not done; done: finished; redo: was done, files changed since; partly: some tasks ready. */
export type ChecklistStepState = "todo" | "done" | "redo" | "partly";

export interface ChecklistStep {
	key: ChecklistStepKey;
	state: ChecklistStepState;
}

export function checklistSteps(suite: Pick<SuiteSummary, "lock_state" | "admitted_count" | "task_count">): ChecklistStep[] {
	const frozen = suite.lock_state === "locked";
	const stale = suite.lock_state === "stale";
	// Freezing runs the file checks first, so a frozen benchmark has passed step 1.
	const lint: ChecklistStepState = frozen || stale ? "done" : "todo";
	const freeze: ChecklistStepState = frozen ? "done" : stale ? "redo" : "todo";
	const ready = suite.admitted_count ?? 0;
	const total = suite.task_count ?? 0;
	const admit: ChecklistStepState = total > 0 && ready >= total ? "done" : ready > 0 ? "partly" : "todo";
	return [
		{ key: "lint", state: lint },
		{ key: "freeze", state: freeze },
		{ key: "admit", state: admit },
	];
}

/** The first step still to do, or null when every step is done. */
export function nextStep(steps: readonly ChecklistStep[]): ChecklistStepKey | null {
	return steps.find((step) => step.state !== "done")?.key ?? null;
}
