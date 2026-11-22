// Agent-first sidebar (lane X09): old pages that became a tab of Settings, Benchmarks, Scheduled
// checks or Releases, and the retired Getting started page. Pure, so the targets are tested
// without a router; each old route's beforeLoad throws redirect() with one.
import type { RedirectTarget, RouteSearch } from "./u3Redirects";

/** search with key removed. */
function without(search: RouteSearch, key: string): RouteSearch {
	const { [key]: _drop, ...rest } = search;
	return rest;
}

/** The old page's search as a tab of another page; a tab it carried gives way to the new one. */
function asTab(to: string, tab: string, search: RouteSearch = {}): RedirectTarget {
	return { to, search: { tab, ...without(search, "tab") } };
}

/** Settings tab ids of the pages that moved there. "runners" is the Workers tab. */
export type SettingsTabTarget = "keys" | "runners" | "usage" | "files" | "jobs" | "integrations" | "operations" | "admin";

/** /models, /workers, /usage, /files, /jobs, /integrations, /operate: their Settings tab, query kept. */
export function settingsRedirect(tab: SettingsTabTarget, search: RouteSearch = {}): RedirectTarget {
	return asTab("/settings", tab, search);
}

/** /admin?tab=users: the Server admin tab of Settings, its own panel tab carried as ?section=. */
export function adminRedirect(search: RouteSearch = {}): RedirectTarget {
	const panel = typeof search.tab === "string" && search.tab ? { section: search.tab } : {};
	return { to: "/settings", search: { tab: "admin", ...panel, ...without(search, "tab") } };
}

/** /upload: the Upload view of the Files tab in Settings. */
export function uploadRedirect(): RedirectTarget {
	return { to: "/settings", search: { tab: "files", files: "upload" } };
}

/** /catalog: the Catalog tab of Benchmarks. */
export function catalogRedirect(search: RouteSearch = {}): RedirectTarget {
	return asTab("/suites", "catalog", search);
}

/** /imports: the Import tab of Benchmarks. /suites/add is no longer redirected (see addBenchmarkTarget). */
export function importTabRedirect(): RedirectTarget {
	return { to: "/suites", search: { tab: "import" } };
}

/** /suites/add is the Add benchmark page (lane Z20), its own route, not a redirect to the Import tab. */
export function addBenchmarkTarget(): RedirectTarget {
	return { to: "/suites/add", search: {} };
}

/** /canaries?name=: the Drift checks tab of Scheduled checks, the open check kept. */
export function canariesRedirect(search: RouteSearch = {}): RedirectTarget {
	return asTab("/scheduled-checks", "drift", search);
}

/** /schedules: the Schedules tab of Scheduled checks. */
export function schedulesRedirect(search: RouteSearch = {}): RedirectTarget {
	return asTab("/scheduled-checks", "schedules", search);
}

/** /review?exp=&reason=&run=: the Needs review tab of Releases, the queue's query kept. */
export function reviewRedirect(search: RouteSearch = {}): RedirectTarget {
	return asTab("/releases", "review", search);
}

/** /policies?file=: the Rules tab of Releases. */
export function policiesRedirect(search: RouteSearch = {}): RedirectTarget {
	return asTab("/releases", "rules", search);
}

/** /welcome (Getting started, retired): /evaluate is the one place to start. */
export function welcomeRedirect(): RedirectTarget {
	return { to: "/evaluate", search: {} };
}
