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

/**
 * The tabs of /failures (M7-1): production failures (the old /inbox list), findings, failure
 * groups (the old /clusters), failure patterns per comparison (the old /triage) and failure
 * patterns per agent version (the old /why-agents-fail).
 */
export const FAILURES_TABS = ["production", "findings", "groups", "patterns", "agents"] as const;
export type FailuresTab = (typeof FAILURES_TABS)[number];

/** Old /inbox tab ids to the /failures tab that holds them; a missing or unknown tab gives production. */
const INBOX_TAB_MAP: Readonly<Record<string, FailuresTab>> = {
	inbox: "production",
	production: "production",
	findings: "findings",
	clusters: "groups",
	groups: "groups",
	triage: "patterns",
	patterns: "patterns",
	agents: "agents",
	tracked: "findings",
};

/**
 * Old /why-agents-fail tab ids. At cc04f6abb its "patterns" tab showed failure patterns per agent
 * version, which is the agents tab now; "Failure patterns" on /failures is the old /inbox triage.
 */
const WHY_TAB_MAP: Readonly<Record<string, FailuresTab>> = {
	patterns: "agents",
	groups: "groups",
	clusters: "groups",
	agents: "agents",
	findings: "findings",
	tracked: "findings",
	production: "production",
	inbox: "production",
	triage: "patterns",
};


/** /inbox?tab=&state=&source=&item=&q=&finding=... becomes /failures with the matching tab, every other key kept. */
export function inboxRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/failures", INBOX_TAB_MAP[text(search.tab) ?? ""] ?? "production", search);
}

/** /why-agents-fail?tab=&agent=&version=&benchmark=&practice=... becomes /failures with the matching tab. */
export function whyAgentsFailRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/failures", WHY_TAB_MAP[text(search.tab) ?? ""] ?? "agents", search);
}

/** /clusters?... becomes the Failure groups tab of /failures (one hop). */
export function clustersRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/failures", "groups", search);
}

/** /triage?exp=&pattern= becomes the Failure patterns tab of /failures (one hop). */
export function triageRedirect(search: RouteSearch): RedirectTarget {
	return asTab("/failures", "patterns", search);
}

/** /imports is the Import tab of Benchmarks (agent-first nav, X09). */
export function importsRedirect(): RedirectTarget {
	return { to: "/suites", search: { tab: "catalog" } };
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