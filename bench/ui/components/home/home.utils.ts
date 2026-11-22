import type { ExperimentStatus, LiveEvent, RunListRow, SuiteSummary } from "@/lib/types/bench";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { isHiddenByDefault } from "@/components/experiments/experiments.utils";
import { suiteReadiness } from "@/components/suites/suiteIdentity";

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

/** Evaluations still queued or running: the ones the Evaluations tile counts as in progress. */
export function isActiveEvaluation(ev: Pick<Evaluation, "status">): boolean {
	return ev.status === "queued" || ev.status === "running";
}

/** Home's "Running now": what is in progress, cut to `limit`, and how many more there are. */
export interface RunningNow<E, X> {
	evaluations: E[];
	comparisons: X[];
	/** Everything in progress, shown or not; the section badge. */
	total: number;
	/** In progress but cut off by the limit. */
	more: number;
}

/**
 * Home's "Running now": every active evaluation (queued or running, newest first), then the running
 * comparisons a person started that belong to no evaluation (an evaluation's benchmarks show through
 * the evaluation). Readiness checks and practice runs stay out. The cut to `limit` comes after the
 * filtering, so the list and the Evaluations tile agree.
 */
export function runningNow<E extends Pick<Evaluation, "status" | "created_at">, X extends ExperimentStatus>(
	evaluations: readonly E[],
	experiments: readonly X[],
	limit = RUNNING_LIMIT,
): RunningNow<E, X> {
	const active = newestEvaluations(evaluations.filter(isActiveEvaluation), Number.POSITIVE_INFINITY);
	const comparisons = byNewest(experiments).filter((exp) => LIVE_STATES.has(exp.state) && !exp.evaluation_id && !isHiddenByDefault(exp));
	const total = active.length + comparisons.length;
	const shownEvaluations = active.slice(0, limit);
	const shownComparisons = comparisons.slice(0, Math.max(0, limit - shownEvaluations.length));
	return {
		evaluations: shownEvaluations,
		comparisons: shownComparisons,
		total,
		more: total - shownEvaluations.length - shownComparisons.length,
	};
}

/**
 * Home's "Recent comparisons": the newest finished experiments that compare two setups. Readiness
 * checks, practice runs and the single-setup benchmarks of an evaluation stay out.
 */
export function recentComparisons<T extends ExperimentStatus>(experiments: readonly T[], limit = RECENT_LIMIT): T[] {
	return byNewest(experiments)
		.filter((exp) => !LIVE_STATES.has(exp.state) && !isHiddenByDefault(exp))
		.slice(0, limit);
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
/**
 * A suite's readiness in the same words as the Benchmarks page (suiteReadiness): "Ready", "24 of 89
 * tasks ready" while only some are admitted, and "Not ready yet" only when none is.
 */
export function suiteHomeState(suite: SuiteSummary): {
	state: "ready" | "attention" | "failed";
	readiness: ReturnType<typeof suiteReadiness>;
} {
	const readiness = suiteReadiness(suite);
	return { state: readiness === "error" ? "failed" : readiness === "ready" ? "ready" : "attention", readiness };
}

/** A suite reads as Ready when it loaded, is locked and every task is admitted; anything else needs attention. */
export function suiteReady(suite: SuiteSummary): boolean {
	return suiteAdmission(suite).tone === "good";
}

const DAY_MS = 86_400_000;

function at(value: string | undefined | null): number {
	const ms = value ? Date.parse(value) : Number.NaN;
	return Number.isFinite(ms) ? ms : Number.NaN;
}

/** First day of the month `now` falls in, local time. */
export function monthStart(now: Date): Date {
	return new Date(now.getFullYear(), now.getMonth(), 1);
}

export interface HomeStats {
	experiments: number;
	running: number;
	/** Runs started in the last seven days, from the newest page of the run list. */
	runsWeek: number;
	/** Counted, sealed runs and how many of them passed. */
	graded: number;
	passed: number;
	/** Share of graded runs that passed, or null with none graded. */
	passRate: number | null;
	/** Spend of the experiments started (or frozen) this month, in USD. */
	spendMonth: number;
}

/** Home's KPI row, read from the experiment list and the newest page of runs. */
export function homeStats(experiments: readonly ExperimentStatus[], runs: readonly RunListRow[], now: Date = new Date()): HomeStats {
	const weekAgo = now.getTime() - 7 * DAY_MS;
	const month = monthStart(now).getTime();
	const runsWeek = runs.filter((run) => at(run.started ?? run.sealed_at) >= weekAgo).length;
	const graded = runs.filter((run) => run.sealed && run.counted);
	const passed = graded.filter((run) => run.outcome === "pass").length;
	const spendMonth = experiments
		.filter((exp) => at(exp.started_at ?? exp.frozen_at) >= month)
		.reduce((sum, exp) => sum + (typeof exp.spent_usd?.v === "number" ? exp.spent_usd.v : 0), 0);
	return {
		experiments: experiments.length,
		running: experiments.filter((exp) => LIVE_STATES.has(exp.state)).length,
		runsWeek,
		graded: graded.length,
		passed,
		passRate: graded.length > 0 ? passed / graded.length : null,
		spendMonth,
	};
}

/** Experiments Heimdall starts on its own to check a task or an agent ("admit-<suite>", "check-<agent>"). */
export function isSystemCheck(exp: Pick<ExperimentStatus, "name" | "exp_id">): boolean {
	return /^(admit|check)-/.test(exp.name ?? "") || /^e_(admit|check)_/.test(exp.exp_id);
}

/**
 * A name a person can read: the experiment's own name with dashes and underscores as spaces,
 * "Readiness check: <target>" for system checks, and never the raw id when a suite is known.
 */
export function humanExperimentName(
	exp: Pick<ExperimentStatus, "name" | "exp_id" | "suite">,
	labels: { check: string; untitled: string },
): string {
	const name = (exp.name ?? "").trim();
	const system = /^(admit|check)-(.+)$/.exec(name);
	if (system) return `${labels.check}: ${system[2]}`;
	if (/^e_(admit|check)_/.test(exp.exp_id)) return labels.check;
	if (name) return name.replace(/[-_]+/g, " ").replace(/^\w/, (c) => c.toUpperCase());
	return labels.untitled;
}

/** Home's agent counts, read from the evaluation list (agent-first contract section 3). */
export interface EvaluationStats {
	/** Evaluations of any status. */
	evaluations: number;
	/** Evaluations still queued or running. */
	active: number;
	/** Distinct agents across every evaluation's arms. */
	agents: number;
}

export function evaluationStats(evaluations: readonly Pick<Evaluation, "status" | "spec">[]): EvaluationStats {
	const agents = new Set<string>();
	for (const ev of evaluations) for (const arm of ev.spec?.arms ?? []) if (arm.agent) agents.add(arm.agent);
	return {
		evaluations: evaluations.length,
		active: evaluations.filter((ev) => ev.status === "queued" || ev.status === "running").length,
		agents: agents.size,
	};
}

/** Newest evaluations first, by creation time. */
export function newestEvaluations<T extends Pick<Evaluation, "created_at">>(evaluations: readonly T[], limit = 5): T[] {
	return [...evaluations].sort((a, b) => (at(b.created_at) || 0) - (at(a.created_at) || 0)).slice(0, limit);
}

/** Set once Home has sent a first-time visitor to /evaluate, so Home shows itself from then on. */
export const FIRST_RUN_KEY = "heimdall.first-run.evaluate";

/** Whether this browser was already sent to /evaluate on a fresh org. Blocked storage counts as seen. */
export function firstRunSeen(): boolean {
	try {
		return window.localStorage.getItem(FIRST_RUN_KEY) === "1";
	} catch {
		return true;
	}
}

export function markFirstRunSeen(): void {
	try {
		window.localStorage.setItem(FIRST_RUN_KEY, "1");
	} catch {
		// Private window or blocked storage: Home stays where it is next time, nothing breaks.
	}
}