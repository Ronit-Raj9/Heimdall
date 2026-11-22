import { suiteDisplayName } from "@/components/suites/suiteDisplay";
import type { ExperimentState, ExperimentStatus } from "@/lib/types/bench";

/** States after which an experiment's status no longer changes, so polling stops. */
export const TERMINAL_STATES: ReadonlySet<ExperimentState> = new Set(["done", "failed", "cancelled"]);

/** The most recently started experiment, else the most recently frozen one. Null for an empty list. */
export function newestExperiment(experiments: readonly ExperimentStatus[]): ExperimentStatus | null {
	let best: ExperimentStatus | null = null;
	let bestAt = Number.NEGATIVE_INFINITY;
	for (const exp of experiments) {
		const at = Date.parse(exp.started_at ?? exp.frozen_at);
		const when = Number.isFinite(at) ? at : Number.NEGATIVE_INFINITY;
		if (best === null || when > bestAt) {
			best = exp;
			bestAt = when;
		}
	}
	return best;
}

// The write token is stored where benchApi reads it for every mutation.
export { readWriteToken, saveWriteToken } from "@/lib/store/apis/benchApi";
/** Kind of an automatic experiment Heimdall starts itself (not a user comparison), else null. */
export type SystemExperimentKind = "readiness" | "agent" | "drift";

/**
 * Detects the internal experiments Heimdall runs on its own: admit-<suite> (benchmark readiness
 * check), check-<agent> (agent check) and canary-<name> (drift monitor). Matches the name, or the
 * id (e_admit_...) when the name is missing. Returns the kind and the subject name. Pure.
 */
export function systemExperiment(e: { name?: string; exp_id: string }): { kind: SystemExperimentKind; subject: string } | null {
	const name = (e.name ?? "").trim();
	const m = /^(admit|check|canary)-(.+)$/i.exec(name) ?? /^e_(admit|check|canary)_(.+?)(?:_[0-9a-z]{4})?$/i.exec(e.exp_id);
	if (!m) return null;
	const kind: SystemExperimentKind = m[1].toLowerCase() === "admit" ? "readiness" : m[1].toLowerCase() === "check" ? "agent" : "drift";
	const subject = name ? m[2] : m[2].replace(/_/g, "-");
	return { kind, subject };
}

/** Suite id without its version: coding-core@1.0.0 to coding-core. */
export function suiteName(suite: string | undefined): string {
	return (suite ?? "").split("@")[0];
}

/**
 * True for an experiment on a practice benchmark (the acme-* examples and coding-core), the same rule
 * as the server's PracticeBenchmark. Practice runs are demos, not results anybody acts on.
 */
export function isPracticeExperiment(e: { suite?: string }): boolean {
	const id = suiteName(e.suite);
	return id.startsWith("acme-") || id === "coding-core";
}

/** True for one benchmark of an evaluation that tests a single setup: it has no B to compare. */
export function isSingleSetupEvaluationRun(e: { evaluation_id?: string; arms?: string[] }): boolean {
	return Boolean(e.evaluation_id) && (e.arms ?? []).filter(Boolean).length < 2;
}

/** A name the server wrote for the user (QuickRun) rather than one the user typed. */
const AUTO_NAME = /^quick-\d{8}-\d{6}(-[0-9a-f]+)?$/i;

/** The name the server gives one benchmark of an evaluation: eval-<date>-<time>-<id>-<suite>. */
const EVAL_CHILD_NAME = /^eval-\d{8}-\d{6}-[0-9a-f]+-/i;

/** True when the experiment's name was generated (or missing), so the UI should build a readable one. */
export function isAutoName(e: { name?: string; exp_id: string }): boolean {
	const name = (e.name ?? "").trim();
	return !name || name === e.exp_id || AUTO_NAME.test(name) || EVAL_CHILD_NAME.test(name);
}

/**
 * An experiment status as the pickers read it, including the debug mode (replay, fork or step)
 * and the run it started from (`debug`, `debug_from_run`).
 */
export type StatusWithDebug = ExperimentStatus;

/** The kinds of debug run the run page can start. */
export type DebugMode = "replay" | "fork" | "step";

/** Old servers do not send `debug`; their debug runs still carry it in the id: e_replay_r_<run>_<suffix>. */
const DEBUG_ID = /^e_(replay|fork|step)_(r_[0-9a-z_]+?)(?:_[0-9a-z]{4})?$/i;

/**
 * The debug mode and source run of a replay, fork or step run, else null. Reads the server's
 * `debug` and `debug_from_run` fields, or the id for older servers. Pure.
 */
export function debugExperiment(e: { exp_id: string; debug?: string; debug_from_run?: string }): { mode: DebugMode; run: string } | null {
	const byId = DEBUG_ID.exec(e.exp_id);
	const raw = (e.debug ?? "").trim().toLowerCase() || byId?.[1]?.toLowerCase();
	if (!raw) return null;
	const mode: DebugMode = raw === "replay" || raw === "fork" ? raw : "step";
	return { mode, run: (e.debug_from_run ?? "").trim() || byId?.[2] || "" };
}

/** True for a replay, fork or step run started from one recorded run: never a comparison. Pure. */
export function isDebugExperiment(e: { exp_id: string; debug?: string; debug_from_run?: string }): boolean {
	return debugExperiment(e) !== null;
}

/** The label keys `humanExperimentName` asks for (the experimentsUx namespace has them all). */
export type ExperimentNameKey =
	| "quickRun"
	| "armsVs"
	| "noSuite"
	| "system.readiness"
	| "system.agent"
	| "system.drift"
	| "evaluationChild"
	| "debug.replay"
	| "debug.fork"
	| "debug.step";

/** Evaluation id to evaluation name, from `useEvaluationNames`. */
export type EvaluationNames = ReadonlyMap<string, string>;

/**
 * The label shown for an experiment. Heimdall's own checks and debug runs say what they are. A
 * benchmark of a known evaluation reads "<evaluation> · <benchmark>". A typed name wins over the
 * rest. A generated one becomes "<arm> vs <arm> on <suite>" when the arm ids say something, else
 * "Quick run on <suite>". Pure.
 */
export function humanExperimentName(
	e: Pick<ExperimentStatus, "exp_id" | "suite" | "arms"> & {
		name?: string;
		evaluation_id?: string;
		debug?: string;
		debug_from_run?: string;
	},
	t: (key: ExperimentNameKey, opts?: Record<string, string>) => string,
	evalNames?: EvaluationNames,
): string {
	// Heimdall's own checks (admit-*, check-*, canary-*) read as what they are, not as comparisons.
	const sys = systemExperiment(e);
	if (sys) return t(`system.${sys.kind}`, { name: sys.subject });
	const dbg = debugExperiment(e);
	if (dbg) return t(`debug.${dbg.mode}`, { run: dbg.run || e.exp_id });
	const evaluation = e.evaluation_id ? evalNames?.get(e.evaluation_id) : undefined;
	if (evaluation) {
		const id = suiteName(e.suite);
		return t("evaluationChild", { evaluation, suite: id ? suiteDisplayName(id) : t("noSuite") });
	}
	if (!isAutoName(e)) return (e.name ?? "").trim();
	const suite = suiteName(e.suite) || t("noSuite");
	const arms = (e.arms ?? []).filter(Boolean);
	// Single-letter ids (A, B) carry no meaning on their own.
	if (arms.length > 0 && arms.some((a) => a.length > 1)) return t("armsVs", { arms: arms.join(" vs "), suite });
	return t("quickRun", { suite });
}

/**
 * What the Comparisons list and Home hide by default: Heimdall's own checks, practice runs, debug
 * runs and the single-setup benchmarks of an evaluation (they live on the evaluation page). Pure.
 */
export function isHiddenByDefault(e: {
	name?: string;
	exp_id: string;
	suite?: string;
	evaluation_id?: string;
	arms?: string[];
	debug?: string;
}): boolean {
	return systemExperiment(e) !== null || isPracticeExperiment(e) || isDebugExperiment(e) || isSingleSetupEvaluationRun(e);
}

/**
 * Not a result anybody reads in a picker unless asked: Heimdall's own checks, practice runs and
 * debug runs. Unlike `isHiddenByDefault`, one-setup benchmarks of an evaluation stay listed. Pure.
 */
export function isCheckPracticeOrDebug(e: { name?: string; exp_id: string; suite?: string; debug?: string }): boolean {
	return systemExperiment(e) !== null || isPracticeExperiment(e) || isDebugExperiment(e);
}

/** One choice of an experiment picker: the readable label and the raw id as grey secondary text. */
export interface ExperimentPickerOption {
	id: string;
	label: string;
	secondary: string;
	state?: ExperimentState;
}

function startedOrFrozen(e: { started_at?: string; frozen_at: string }): number {
	const ms = Date.parse(e.started_at ?? e.frozen_at);
	return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
}

function pickerOption(
	e: StatusWithDebug,
	t: Parameters<typeof humanExperimentName>[1],
	evalNames?: EvaluationNames,
): ExperimentPickerOption {
	return { id: e.exp_id, label: humanExperimentName(e, t, evalNames), secondary: e.exp_id, state: e.state };
}

/**
 * The choices of the review and triage pickers: newest first, readable names, and checks,
 * practice runs and debug runs left out unless `showHidden`. Pure.
 */
export function experimentPickerOptions(
	experiments: readonly StatusWithDebug[],
	t: Parameters<typeof humanExperimentName>[1],
	evalNames?: EvaluationNames,
	opts: { showHidden?: boolean } = {},
): ExperimentPickerOption[] {
	return [...experiments]
		.sort((x, y) => startedOrFrozen(y) - startedOrFrozen(x))
		.filter((e) => opts.showHidden || !isCheckPracticeOrDebug(e))
		.map((e) => pickerOption(e, t, evalNames));
}

/**
 * The choices of the Download results picker in three groups, newest first in each: benchmarks of
 * an evaluation (one-setup ones included), comparisons, and checks, practice and debug runs (empty
 * unless `showHidden`). Pure.
 */
export function exportPickerGroups(
	experiments: readonly StatusWithDebug[],
	t: Parameters<typeof humanExperimentName>[1],
	evalNames?: EvaluationNames,
	showHidden = false,
): { evaluations: ExperimentPickerOption[]; comparisons: ExperimentPickerOption[]; other: ExperimentPickerOption[]; hiddenCount: number } {
	const out = {
		evaluations: [] as ExperimentPickerOption[],
		comparisons: [] as ExperimentPickerOption[],
		other: [] as ExperimentPickerOption[],
		hiddenCount: 0,
	};
	for (const e of [...experiments].sort((x, y) => startedOrFrozen(y) - startedOrFrozen(x))) {
		if (systemExperiment(e) !== null || isDebugExperiment(e)) {
			out.hiddenCount++;
			if (showHidden) out.other.push(pickerOption(e, t, evalNames));
		} else if (e.evaluation_id) out.evaluations.push(pickerOption(e, t, evalNames));
		else if (isPracticeExperiment(e)) {
			out.hiddenCount++;
			if (showHidden) out.other.push(pickerOption(e, t, evalNames));
		} else out.comparisons.push(pickerOption(e, t, evalNames));
	}
	return out;
}