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

const TOKEN_KEY = "bifrost-bench.write-token";

/**
 * The write token lives only in this tab's sessionStorage: never in the URL, Redux or
 * localStorage. Storage can be unavailable (private mode, tests), so every access is guarded.
 */
export function readWriteToken(): string {
	try {
		return typeof window === "undefined" ? "" : (window.sessionStorage.getItem(TOKEN_KEY) ?? "");
	} catch {
		return "";
	}
}

export function saveWriteToken(token: string): void {
	try {
		if (typeof window === "undefined") return;
		if (token) window.sessionStorage.setItem(TOKEN_KEY, token);
		else window.sessionStorage.removeItem(TOKEN_KEY);
	} catch {
		// Storage blocked: the token still works for this page view.
	}
}