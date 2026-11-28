import type { Evaluation } from "@/lib/store/apis/evaluationsApi";

/** What remains of a cap: cap minus spent, never below 0; null when there is no cap (0 or unset). */
export function capLeft(capUsd: number | null | undefined, spentUsd: number | null | undefined): number | null {
	if (typeof capUsd !== "number" || !Number.isFinite(capUsd) || capUsd <= 0) return null;
	const spent = typeof spentUsd === "number" && Number.isFinite(spentUsd) ? spentUsd : 0;
	return Math.max(0, capUsd - spent);
}

/**
 * The cap and what is left of it for resuming an evaluation (K5, PQ1-5): the running children's own
 * caps minus their spend, the same numbers the server checks. Falls back to the evaluation's budget
 * minus all children's spend when a child has no cap of its own. Null when nothing is capped.
 */
export function evaluationResumeCap(ev: Pick<Evaluation, "children" | "spec">): { cap: number; left: number } | null {
	const kids = (ev.children ?? []).filter((c) => c.status === "running");
	const spent = (ev.children ?? []).reduce((s, c) => s + (c.spent_usd ?? 0), 0);
	if (kids.length > 0 && kids.every((c) => (c.budget_usd ?? 0) > 0)) {
		const cap = kids.reduce((s, c) => s + (c.budget_usd ?? 0), 0);
		const left = kids.reduce((s, c) => s + (capLeft(c.budget_usd, c.spent_usd) ?? 0), 0);
		return { cap, left };
	}
	const budget = ev.spec?.budget_usd;
	const left = capLeft(budget, spent);
	return left === null ? null : { cap: budget ?? 0, left };
}