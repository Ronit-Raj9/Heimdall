// The runs list remembers its last filters for this tab, so the run page crumb leads back to the
// same filtered list (M5-7). Session storage only: a convenience, never needed for the page to work.

const KEY = "heimdall.runs.lastSearch";

/** The filter keys worth carrying back; paging and sort reset are fine to keep too. */
const KEPT = ["outcome", "suite", "agent", "fault", "q", "tq", "sort", "dir", "page", "archived"] as const;

/** Keeps only the known list filters of a query string, or undefined when none is set. Pure. */
export function runsSearchFrom(query: string): Record<string, string> | undefined {
	const params = new URLSearchParams(query);
	const out: Record<string, string> = {};
	for (const key of KEPT) {
		const value = params.get(key);
		if (value) out[key] = value;
	}
	return Object.keys(out).length > 0 ? out : undefined;
}

/** Saves the runs list query string (call from the list page). */
export function saveRunsSearch(query: string): void {
	try {
		window.sessionStorage.setItem(KEY, query);
	} catch {
		// No storage (private window, blocked): the crumb then opens the plain list.
	}
}

/** The last saved runs list filters, or undefined. */
export function savedRunsSearch(): Record<string, string> | undefined {
	try {
		const query = window.sessionStorage.getItem(KEY);
		return query ? runsSearchFrom(query) : undefined;
	} catch {
		return undefined;
	}
}
