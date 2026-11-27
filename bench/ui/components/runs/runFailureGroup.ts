import type { RunFailureGroup } from "@/lib/types/serverFields";

export type { RunFailureGroup };

/**
 * The "same failure in N runs" link, or undefined when the group is missing, has no signature, or holds
 * only this run. It opens the failure groups of the evaluation with that group selected.
 */
export function sameFailureLink(
	failure: RunFailureGroup | undefined,
	fallbackExpId?: string,
): { count: number; search: { tab: "clusters"; sig: string; exp?: string } } | undefined {
	const count = failure?.count ?? 0;
	if (!failure?.signature || !Number.isFinite(count) || count < 2) return undefined;
	const exp = failure.exp_id || fallbackExpId;
	return { count, search: { tab: "clusters", sig: failure.signature, ...(exp ? { exp } : {}) } };
}
