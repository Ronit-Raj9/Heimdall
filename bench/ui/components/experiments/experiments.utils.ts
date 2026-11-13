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