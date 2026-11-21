// U3 old routes (UI regroup): where each page that became a tab or a flow now lives. Pure, so the
// targets are tested without a router; each old route's beforeLoad throws redirect() with one.

/** A route search object as TanStack Router parses it. */
export type RouteSearch = Record<string, unknown>;

/** A redirect target: a route path, its path params and its search. */
export interface RedirectTarget {
	to: string;
	params?: Record<string, string>;
	search: RouteSearch;
}

/** A non-empty string search value, else null. */
function text(value: unknown): string | null {
	if (typeof value === "string") return value.trim() ? value : null;
	if (typeof value === "number" || typeof value === "boolean") return String(value);
	return null;
}

/** search with key removed. */
function without(search: RouteSearch, key: string): RouteSearch {
	const { [key]: _drop, ...rest } = search;
	return rest;
}

/** The old page's search as a tab of another page; any tab it carried gives way to the new one. */
function asTab(to: string, tab: string, search: RouteSearch): RedirectTarget {
	return { to, search: { tab, ...without(search, "tab") } };
}

/** /agents/diff?a=&b=&... becomes the Diff tab of /agents, its query kept. */
export function agentDiffRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/agents", "diff", search);
}

/** /trends?... becomes the Trends tab of /leaderboard. */
export function trendsRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/leaderboard", "trends", search);
}

/** /clusters?... becomes the Clusters tab of /inbox. */
export function clustersRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/inbox", "clusters", search);
}

/** /triage?exp=&pattern= becomes the Triage tab of /inbox. */
export function triageRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/inbox", "triage", search);
}

/** /imports is the Import tab of Benchmarks (agent-first nav, X09). */
export function importsRedirect(): RedirectTarget {
	return { to: "/suites", search: { tab: "import" } };
}

/** /calibration?suite=X is the Calibration tab of /suites/X; with no suite, the suites list. */
export function calibrationRedirect(search: RouteSearch): RedirectTarget {
	const suite = text(search.suite);
	if (!suite) return { to: "/suites", search: {} };
	return {
		to: "/suites/$suiteId",
		params: { suiteId: suite },
		search: { tab: "calibration", ...without(without(search, "suite"), "tab") },
	};
}

/**
 * /suites?suite=X&task=&kind=... (the old one-page suites screen) is /suites/X with the task
 * table's query kept; null when there is no suite to move to, so the list renders.
 */
export function suitesListRedirect(search: RouteSearch): RedirectTarget | null {
	const suite = text(search.suite);
	if (!suite) return null;
	return { to: "/suites/$suiteId", params: { suiteId: suite }, search: without(search, "suite") };
}