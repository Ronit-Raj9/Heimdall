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
