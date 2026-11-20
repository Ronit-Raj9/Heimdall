// Old per-feature URLs (UI regroup, U2) and where each one lives now. The old route files call
// these from beforeLoad, so a bookmark or a link from another page lands on the right tab.
// Everything here is pure so the table can be tested without a router.

/** Where an old URL goes: a route path, its params and its search. */
export interface LegacyTarget {
	to: "/experiments" | "/experiments/$expId" | "/compare";
	params?: { expId: string };
	search?: Record<string, string>;
}

/** The old screens that became tabs of an experiment, and the tab each one became. */
export const LEGACY_EXPERIMENT_TABS = {
	"/verdict": null,
	"/receipts": "receipts",
	"/matrix": "matrix",
	"/migration": "migration",
} as const;

export type LegacyExperimentPath = keyof typeof LEGACY_EXPERIMENT_TABS;

/** Search values as strings. The router may parse "0.05" into a number; the new pages read strings. */
export function stringSearch(search: Record<string, unknown>): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [key, value] of Object.entries(search)) {
		if (value === undefined || value === null || value === "") continue;
		out[key] = typeof value === "string" ? value : JSON.stringify(value);
	}
	return out;
}

/**
 * /verdict?exp=X, /receipts?exp=X, /matrix?exp=X and /migration?exp=X go to the experiment's tab;
 * every other parameter (grade, condition, run, from, to, alpha, grade_source) comes along. Without
 * exp they go to the experiment list.
 */
export function legacyExperimentTarget(path: LegacyExperimentPath, rawSearch: Record<string, unknown>): LegacyTarget {
	// A stale tab in the old URL never overrides the page the bookmark names (or the verdict default).
	const { exp, tab: _staleTab, ...rest } = stringSearch(rawSearch);
	if (!exp) return { to: "/experiments" };
	const tab = LEGACY_EXPERIMENT_TABS[path];
	const search = tab ? { tab, ...rest } : rest;
	return { to: "/experiments/$expId", params: { expId: exp }, search };
}

/** /runs?pair=P (the old pair view, often with exp) goes to /compare with the same parameters; null when there is no pair. */
export function legacyRunsTarget(rawSearch: Record<string, unknown>): LegacyTarget | null {
	const search = stringSearch(rawSearch);
	if (!search.pair) return null;
	return { to: "/compare", search };
}

/** The search of the pair comparison: one matched pair, and its experiment when known. */
export function compareSearch(pairId: string | undefined, expId?: string): Record<string, string> {
	const search: Record<string, string> = {};
	if (pairId) search.pair = pairId;
	if (expId) search.exp = expId;
	return search;
}

/** The pair comparison of one matched pair, as an href. */
export function compareHref(pairId: string, expId?: string): string {
	return `/compare?${new URLSearchParams(compareSearch(pairId, expId)).toString()}`;
}