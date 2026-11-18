import type { ExperimentStatus, LiveEvent, SuiteSummary } from "@/lib/types/bench";

/** Experiments whose slots are still being run or analyzed. */
export const LIVE_STATES: ReadonlySet<ExperimentStatus["state"]> = new Set(["running", "analyzing"]);

/** How many running experiments Home follows at once, and how many recent ones it lists. */
export const RUNNING_LIMIT = 4;
export const RECENT_LIMIT = 6;
/** Events kept per running experiment. */
export const EVENTS_KEEP = 20;

/**
 * An experiment as the list may carry it. The list endpoint may add the suite and the newest
 * verdict decision; Home uses them when present and asks the verdict route otherwise.
 */
export type ExperimentListItem = ExperimentStatus & { suite?: string; decision?: string };

function when(exp: ExperimentStatus): number {
	const at = Date.parse(exp.started_at ?? exp.frozen_at);
	return Number.isFinite(at) ? at : Number.NEGATIVE_INFINITY;
}

/** Newest first by start time, else by freeze time; ties by id so the order is stable. */
export function byNewest<T extends ExperimentStatus>(experiments: readonly T[]): T[] {
	return [...experiments].sort((a, b) => when(b) - when(a) || a.exp_id.localeCompare(b.exp_id));
}

/** Home's two experiment lists: the running ones, and the newest of the rest. */
export function splitExperiments<T extends ExperimentStatus>(experiments: readonly T[]): { running: T[]; recent: T[] } {
	const sorted = byNewest(experiments);
	return {
		running: sorted.filter((exp) => LIVE_STATES.has(exp.state)).slice(0, RUNNING_LIMIT),
		recent: sorted.filter((exp) => !LIVE_STATES.has(exp.state)).slice(0, RECENT_LIMIT),
	};
}

/** Appends events after the newest seq seen, in order, keeping the last `keep`. */
export function mergeEvents(current: readonly LiveEvent[], incoming: readonly LiveEvent[], keep = EVENTS_KEEP): LiveEvent[] {
	const last = current.length > 0 ? current[current.length - 1].seq : -1;
	const fresh = incoming.filter((event) => event.seq > last).sort((a, b) => a.seq - b.seq);
	if (fresh.length === 0) return current as LiveEvent[];
	const next = [...current, ...fresh];
	return next.length > keep ? next.slice(next.length - keep) : next;
}

/** Share of slots in each part of the progress bar, as CSS widths. */
export function slotShares(slots: ExperimentStatus["slots"]): { done: string; failed: string; running: string } {
	const share = (n: number) => (slots.total > 0 ? `${Math.min(100, Math.max(0, (n / slots.total) * 100))}%` : "0%");
	return { done: share(slots.done), failed: share(slots.failed), running: share(slots.running) };
}

/** A suite's admission state for Home: tasks admitted out of all, and the lock's tone. */
export function suiteAdmission(suite: SuiteSummary): {
	admitted: number;
	total: number;
	share: number;
	tone: "good" | "warn" | "neutral" | "bad";
} {
	if (suite.error) return { admitted: 0, total: 0, share: 0, tone: "bad" };
	const total = suite.task_count;
	const admitted = suite.admitted_count;
	const share = total > 0 ? admitted / total : 0;
	const tone =
		suite.lock_state === "locked" && admitted === total && total > 0 ? "good" : suite.lock_state === "stale" ? "warn" : "neutral";
	return { admitted, total, share, tone };
}