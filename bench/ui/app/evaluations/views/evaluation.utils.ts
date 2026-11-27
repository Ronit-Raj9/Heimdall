import { isTestObject } from "@/lib/testObjects";
import type { StatusTone } from "@/components/ui/statusPill";
import type { Evaluation, EvaluationArm, EvaluationChild } from "@/lib/store/apis/evaluationsApi";

/** How often a running evaluation (and its running benchmarks) is polled. */
export const EVALUATION_POLL_MS = 3000;

const KNOWN_MODES = ["score", "versions", "models", "opponent", "model_switch"] as const;
const KNOWN_STATUSES = ["queued", "created", "running", "analyzing", "done", "failed", "cancelled", "skipped", "notStarted"] as const;

/** The i18n key (evaluations namespace) of a mode in plain words. */
export function modeKey(mode: string): `mode.${(typeof KNOWN_MODES)[number]}` | null {
	return (KNOWN_MODES as readonly string[]).includes(mode) ? (`mode.${mode}` as `mode.${(typeof KNOWN_MODES)[number]}`) : null;
}

/** The i18n key of a status word, or null for a status this UI does not know. */
export function statusKey(status: string): `status.${(typeof KNOWN_STATUSES)[number]}` | null {
	return (KNOWN_STATUSES as readonly string[]).includes(status)
		? (`status.${status}` as `status.${(typeof KNOWN_STATUSES)[number]}`)
		: null;
}

/** Still moving: keep polling. */
export function isActiveStatus(status: string): boolean {
	return status === "queued" || status === "created" || status === "running" || status === "analyzing";
}

export function statusTone(status: string): StatusTone {
	switch (status) {
		case "done":
			return "ok";
		case "failed":
		case "notStarted":
			return "bad";
		// Cancelled is a stop the user chose, not a warning: neutral like everywhere else (M3-25).
		case "cancelled":
			return "neutral";
		case "skipped":
			return "warn";
		case "running":
		case "analyzing":
			return "running";
		default:
			return "neutral";
	}
}

/** "codex v2" or "codex" when no version is pinned. */
export function armAgentLabel(arm: EvaluationArm): string {
	return arm.version ? `${arm.agent} v${arm.version}` : arm.agent;
}

/** The distinct agents of the arms, in order, for the list's Agent column. */
export function evaluationAgents(evaluation: Evaluation): string[] {
	const out: string[] = [];
	for (const arm of evaluation.spec.arms ?? []) {
		const label = armAgentLabel(arm);
		if (!out.includes(label)) out.push(label);
	}
	return out;
}

/** Children that were run (have an experiment) and children that were skipped, apart. */
export function splitChildren(children: readonly EvaluationChild[] | null | undefined): {
	run: EvaluationChild[];
	skipped: EvaluationChild[];
} {
	const run: EvaluationChild[] = [];
	const skipped: EvaluationChild[] = [];
	for (const child of children ?? []) {
		if (child.skipped || !child.experiment_id) skipped.push(child);
		else run.push(child);
	}
	return { run, skipped };
}

/** Benchmarks count: the children when present, else the requested list. */
export function benchmarkCount(evaluation: Evaluation): number {
	const children = evaluation.children ?? [];
	return children.length > 0 ? children.length : (evaluation.spec.benchmarks ?? []).length;
}

/** Newest first, whatever order the server sent. */
export function sortNewestFirst(list: readonly Evaluation[]): Evaluation[] {
	return [...list].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
}

/** A date in the viewer's locale; the raw text when it does not parse. */
export function formatCreated(iso: string | undefined, locale?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
}
/** The status to show: "paused" while a running evaluation has every running benchmark paused. */
export function displayStatus(evaluation: Evaluation): string {
	return evaluation.status === "running" && evaluation.paused ? "paused" : evaluation.status;
}

/**
 * A failed evaluation where nothing started (M3-10): no stored result and no benchmark spent anything or ran, so the
 * page and list say "Did not start" instead of "Failed".
 */
export function didNotStart(evaluation: Evaluation): boolean {
	if (evaluation.status !== "failed" || evaluation.result) return false;
	const kids = evaluation.children ?? [];
	return kids.every((c) => !!c.skipped || !c.experiment_id || !((c.spent_usd ?? 0) > 0));
}

/** The status word for an evaluation's pill: "notStarted" for a failure where nothing ran, else its status. */
export function evaluationStatusWord(evaluation: Evaluation): string {
	return didNotStart(evaluation) ? "notStarted" : evaluation.status;
}

/** A known cause of a benchmark that could not run, from the worker's raw message (M3-9). */
export type FailureCause = "imageMissing" | "workerRefused" | "noWorker";

const FAILURE_CAUSES: { cause: FailureCause; test: RegExp }[] = [
	{ cause: "imageMissing", test: /image[^.]{0,80}(missing|not found|not on|no such|never built|not built|pull)|registry-sync|manifest unknown/i },
	{ cause: "noWorker", test: /no (online |available )?worker|worker[^.]{0,40}offline|no runner/i },
	{ cause: "workerRefused", test: /refus|rejected|denied/i },
];

/** The cause codes a newer server sends on each child (G1, `cause`); an older server sends none. */
const CAUSE_CODES: Record<string, FailureCause> = {
	image_missing: "imageMissing",
	worker_refused: "workerRefused",
	no_worker: "noWorker",
};

/** A child with the server's cause code (G1); absent on an older server. */
export type ChildWithCause = EvaluationChild & { cause?: string };

/**
 * The cause of a raw run problem: the server's cause code when it sends a known one (M3-9), else the raw text
 * matched against the known messages; undefined when neither says.
 */
export function failureCause(raw: string | undefined | null, code?: string | null): FailureCause | undefined {
	if (code && CAUSE_CODES[code]) return CAUSE_CODES[code];
	if (!raw) return undefined;
	return FAILURE_CAUSES.find((c) => c.test.test(raw))?.cause;
}

/** Why each benchmark did not run: its skip reason, else its status. */
export function childReasons(
	children: readonly EvaluationChild[] | null | undefined,
): { benchmark: string; reason: string; cause?: string }[] {
	return (children ?? []).map((c) => {
		const cause = (c as ChildWithCause).cause;
		return { benchmark: c.benchmark, reason: c.skipped || c.status, ...(cause ? { cause } : {}) };
	});
}

/** The evaluation's budget: the sum of its launched benchmarks' caps (raised anywhere), else the spec's. */
export function evaluationBudget(evaluation: Evaluation): number {
	let total = 0;
	for (const c of evaluation.children ?? []) {
		if (c.experiment_id && c.status !== "skipped") total += c.budget_usd ?? 0;
	}
	return total > 0 ? total : (evaluation.spec.budget_usd ?? 0);
}

/** The benchmarks of an evaluation: its children, else the requested list. */
export function evaluationBenchmarks(evaluation: Evaluation): string[] {
	const children = evaluation.children ?? [];
	return children.length > 0 ? children.map((c) => c.benchmark) : [...(evaluation.spec.benchmarks ?? [])];
}

export const EVALUATION_STATUS_FILTERS = ["all", "running", "done", "failed", "cancelled"] as const;
export const EVALUATION_SORTS = ["newest", "oldest", "name", "spend", "result"] as const;
export type EvaluationStatusFilter = (typeof EVALUATION_STATUS_FILTERS)[number];
export type EvaluationSort = (typeof EVALUATION_SORTS)[number];

/** The mode filter value of a failure test: a score evaluation with faults (the Failure tests page launches these). */
export const FAILURE_TEST_MODE = "failure_test";

/** What an evaluation tests: FAILURE_TEST_MODE when it injects any fault, else its mode. */
export function evaluationKind(evaluation: Evaluation): string {
	const faulted = (evaluation.spec?.faults ?? []).some((f) => f !== "clean");
	return faulted && evaluation.spec.mode === "score" ? FAILURE_TEST_MODE : String(evaluation.spec.mode);
}

/** The stored result in short: "56%" for one arm, "A 40% · B 45%" for two, "-" without one. */
export function evaluationResultText(evaluation: Evaluation): string {
	const arms = evaluation.result?.arms ?? [];
	if (arms.length === 0) return "-";
	const pc = (x: number) => `${Math.round(x * 100)}%`;
	return arms.length === 1 ? pc(arms[0].score) : arms.map((a) => `${a.label} ${pc(a.score)}`).join(" · ");
}

/** The best arm score of the stored result, or null without one. */
function bestResult(evaluation: Evaluation): number | null {
	const arms = evaluation.result?.arms ?? [];
	return arms.length === 0 ? null : Math.max(...arms.map((a) => a.score));
}

/** Spend: the stored result's cost, else the sum of the benchmarks' spend so far, else null. */
export function evaluationSpend(evaluation: Evaluation): number | null {
	// Same order as evaluationTotals on the evaluation page: what the benchmarks spent (every run, normal and
	// with faults) first, then the stored result. The list and the page then show one cost (N3-1).
	if ((evaluation.children ?? []).some((c) => typeof c.spent_usd === "number")) return spentOf(evaluation);
	if (typeof evaluation.result?.cost_usd === "number") return evaluation.result.cost_usd;
	return null;
}

type RunsKey = "detail.runs" | "detail.runsWithFailed" | "detail.runsRunning";

/**
 * One benchmark's run count: "29 of 30 runs done", or with runs that could not run (an infrastructure error)
 * "29 of 30 runs scored, 1 could not run (infrastructure error)"; while active, ", 3 running" is added.
 */
export function childRunsText(
	t: (key: RunsKey, opts: Record<string, number>) => string,
	slots: { total: number; done: number; running: number; failed: number },
	active: boolean,
): string {
	const base =
		slots.failed > 0
			? t("detail.runsWithFailed", { done: slots.done, total: slots.total, count: slots.failed })
			: t("detail.runs", { done: slots.done, total: slots.total });
	return active && slots.running > 0 ? base + t("detail.runsRunning", { running: slots.running }) : base;
}

/** The list's search, filters and sort, kept in the URL (`q`, `status`, `mode`, `sort`). */
export interface EvaluationFilters {
	q: string;
	status: EvaluationStatusFilter;
	/** "all" or a mode id. */
	mode: string;
	sort: EvaluationSort;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
	return typeof value === "string" && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

/** Reads the filters from URL search values; unknown values fall back to the defaults. */
export function parseEvaluationFilters(search: Record<string, unknown> | null | undefined): EvaluationFilters {
	const s = search ?? {};
	return {
		q: typeof s.q === "string" ? s.q.trim() : "",
		status: oneOf(s.status, EVALUATION_STATUS_FILTERS, "all"),
		mode: typeof s.mode === "string" && s.mode !== "" ? s.mode : "all",
		sort: oneOf(s.sort, EVALUATION_SORTS, "newest"),
	};
}

/** The URL search values of the filters, leaving out the defaults. */
export function evaluationFiltersSearch(f: EvaluationFilters): Record<string, string | undefined> {
	return {
		q: f.q || undefined,
		status: f.status === "all" ? undefined : f.status,
		mode: f.mode === "all" ? undefined : f.mode,
		sort: f.sort === "newest" ? undefined : f.sort,
	};
}

function spentOf(evaluation: Evaluation): number {
	return (evaluation.children ?? []).reduce((s, c) => s + (c.spent_usd ?? 0), 0);
}

function matchesStatus(evaluation: Evaluation, status: EvaluationStatusFilter): boolean {
	if (status === "all") return true;
	if (status === "running") return isActiveStatus(evaluation.status);
	return evaluation.status === status;
}

/** Search (name, id, agent, benchmark), status and mode filters, then the sort. */
export function filterEvaluations(list: readonly Evaluation[], f: EvaluationFilters): Evaluation[] {
	const q = f.q.toLowerCase();
	const out = list.filter((ev) => {
		if (!matchesStatus(ev, f.status)) return false;
		if (f.mode !== "all" && evaluationKind(ev) !== f.mode) return false;
		if (!q) return true;
		const hay = [
			ev.spec.name ?? "",
			ev.id,
			...evaluationAgents(ev),
			...(ev.spec.arms ?? []).map((a) => a.agent),
			...evaluationBenchmarks(ev),
		];
		return hay.some((h) => h.toLowerCase().includes(q));
	});
	switch (f.sort) {
		case "oldest":
			return sortNewestFirst(out).reverse();
		case "name":
			return out.sort((a, b) => (a.spec.name || a.id).localeCompare(b.spec.name || b.id));
		case "spend":
			return sortNewestFirst(out).sort((a, b) => (evaluationSpend(b) ?? 0) - (evaluationSpend(a) ?? 0));
		case "result":
			return sortNewestFirst(out).sort((a, b) => (bestResult(b) ?? -1) - (bestResult(a) ?? -1));
		default:
			return sortNewestFirst(out);
	}
}
/** An evaluation the project's own checks made: a test name or id, or a test agent in an arm (M1-18). Pure. */
export function isTestEvaluation(ev: Pick<Evaluation, "id" | "spec">): boolean {
	return isTestObject(ev.spec?.name) || isTestObject(ev.id) || (ev.spec?.arms ?? []).some((a) => isTestObject(a.agent));
}
