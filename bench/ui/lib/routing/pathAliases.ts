/**
 * Addresses shaped like a sidebar label ("/live-sessions" for Live sessions) and other words people
 * type, mapped to the page that has them. They have no route file of their own: the not-found page
 * redirects them (fix wave 2, N129 and N73). Targets may carry a query string.
 */
export const PATH_ALIASES: Readonly<Record<string, string>> = {
	"/home": "/",
	"/my-agents": "/agents",
	"/comparisons": "/experiments",
	"/test-my-mcp-server": "/mcp-test",
	"/mcp-server-test": "/mcp-test",
	"/production-failures": "/failures?tab=production",
	"/live-scoring": "/live",
	"/checks": "/scheduled-checks",
	"/safety-rule-tests": "/monitors",
	"/human-review": "/annotation",
	"/guides": "/docs",
	"/auto-improve": "/improvements",
	"/runs-and-traces": "/runs",
	"/benchmarks": "/suites",
	"/live-sessions": "/live",
	"/labelling": "/annotation",
	"/labeling": "/annotation",
	"/drift": "/scheduled-checks?tab=drift",
	"/get-started": "/quickstart",
	"/safety-monitors": "/monitors",
};

/** The page an alias address stands for (exact match after dropping one trailing "/"), or undefined. */
export function aliasFor(pathname: string): string | undefined {
	const path = pathname.length > 1 && pathname.endsWith("/") ? pathname.slice(0, -1) : pathname;
	return Object.prototype.hasOwnProperty.call(PATH_ALIASES, path) ? PATH_ALIASES[path] : undefined;
}

/** The address a sidebar label would have: lower case, runs of other characters to one "-". */
export function labelSlug(label: string): string {
	return `/${label
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")}`;
}

/**
 * Where the not-found page sends an alias address: the target path, with the target's own query
 * and then the typed one ("/drift?x=1" gives /scheduled-checks with tab=drift and x=1).
 */
export function aliasRedirect(
	pathname: string,
	search: Readonly<Record<string, unknown>> = {},
): { to: string; search: Record<string, unknown> } | undefined {
	const target = aliasFor(pathname);
	if (!target) return undefined;
	const [to = "/", query = ""] = target.split("?");
	return { to, search: { ...Object.fromEntries(new URLSearchParams(query)), ...search } };
}