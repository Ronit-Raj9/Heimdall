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

/** True when the experiment's name was generated (or missing), so the UI should build a readable one. */
export function isAutoName(e: { name?: string; exp_id: string }): boolean {
	const name = (e.name ?? "").trim();
	return !name || name === e.exp_id || AUTO_NAME.test(name);
}

/** The label keys `humanExperimentName` asks for (the experimentsUx namespace has them all). */
export type ExperimentNameKey = "quickRun" | "armsVs" | "noSuite" | "system.readiness" | "system.agent" | "system.drift";

/**
 * The label shown for an experiment. A typed name wins. A generated one becomes
 * "<arm> vs <arm> on <suite>" when the arm ids say something, else "Quick run on <suite>". Pure.
 */
export function humanExperimentName(
	e: Pick<ExperimentStatus, "exp_id" | "suite" | "arms"> & { name?: string },
	t: (key: ExperimentNameKey, opts?: Record<string, string>) => string,
): string {
	// Heimdall's own checks (admit-*, check-*, canary-*) read as what they are, not as comparisons.
	const sys = systemExperiment(e);
	if (sys) return t(`system.${sys.kind}`, { name: sys.subject });
	if (!isAutoName(e)) return (e.name ?? "").trim();
	const suite = suiteName(e.suite) || t("noSuite");
	const arms = (e.arms ?? []).filter(Boolean);
	// Single-letter ids (A, B) carry no meaning on their own.
	if (arms.length > 0 && arms.some((a) => a.length > 1)) return t("armsVs", { arms: arms.join(" vs "), suite });
	return t("quickRun", { suite });
}

/**
 * What the Comparisons list and Home hide by default: Heimdall's own checks, practice runs and the
 * single-setup benchmarks of an evaluation (they live on the evaluation page). Pure.
 */
export function isHiddenByDefault(e: { name?: string; exp_id: string; suite?: string; evaluation_id?: string; arms?: string[] }): boolean {
	return systemExperiment(e) !== null || isPracticeExperiment(e) || isSingleSetupEvaluationRun(e);
}
