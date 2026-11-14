// Fixture mode: with ?fixture=1 or VITE_BENCH_FIXTURE=1, RTK Query reads public/fixtures/*.json
// instead of calling the server, so screens can be built with no bench server running.
// Everything here is pure so it can be tested without a browser.

/** Prefix of every bench API route (DESIGN.md 16.7). */
export const BENCH_API_BASE = "/api/bench";

/** One API route and the fixture file that answers it. `{id}` in the file is the path id. */
export interface FixtureRoute {
	method: "GET" | "POST";
	/** Path below BENCH_API_BASE; ":id" matches one path segment. */
	pattern: string;
	file: string;
}

/**
 * Every route in DESIGN.md 16.7 that returns JSON. Experiment-level routes share one fixture
 * because fixtures hold one experiment; run and pair routes are keyed by id because a pair has
 * two runs. GET /experiments/{id}/events is server-sent events and has no fixture: in fixture
 * mode screens poll GET /experiments/{id}, which is the designed fallback.
 */
export const FIXTURE_ROUTES: readonly FixtureRoute[] = [
	{ method: "GET", pattern: "/health", file: "health.json" },
	{ method: "GET", pattern: "/doctor", file: "doctor.json" },
	{ method: "GET", pattern: "/catalog", file: "catalog.json" },
	{ method: "POST", pattern: "/experiments/plan", file: "experiments.plan.json" },
	{ method: "POST", pattern: "/experiments", file: "experiments.create.json" },
	{ method: "POST", pattern: "/experiments/:id/start", file: "experiments.start.json" },
	{ method: "GET", pattern: "/experiments", file: "experiments.list.json" },
	{ method: "GET", pattern: "/experiments/:id", file: "experiments.get.json" },
	{ method: "GET", pattern: "/experiments/:id/verdict", file: "experiments.verdict.json" },
	{ method: "GET", pattern: "/experiments/:id/diagnostics", file: "experiments.diagnostics.json" },
	{ method: "GET", pattern: "/experiments/:id/grid", file: "experiments.grid.json" },
	{ method: "GET", pattern: "/experiments/:id/triage", file: "experiments.triage.json" },
	{ method: "GET", pattern: "/runs/:id", file: "runs.{id}.json" },
	{ method: "GET", pattern: "/runs/:id/events", file: "runs.{id}.events.json" },
	{ method: "GET", pattern: "/runs/:id/diff", file: "runs.{id}.diff.json" },
	{ method: "GET", pattern: "/runs/:id/receipts", file: "runs.{id}.receipts.json" },
	{ method: "GET", pattern: "/pairs/:id", file: "pairs.{id}.json" },
	{ method: "POST", pattern: "/audit", file: "audit.json" },
	{ method: "POST", pattern: "/regrade", file: "regrade.json" },
	{ method: "GET", pattern: "/releases", file: "releases.list.json" },
];

/** True when the URL has fixture=1 or the build sets VITE_BENCH_FIXTURE=1. */
export function detectFixtureMode(search: string, envFlag: string | undefined): boolean {
	if (envFlag === "1") return true;
	return new URLSearchParams(search).get("fixture") === "1";
}

/** Fixture file name for a request, or null when no route matches. Ignores the query string. */
export function resolveFixture(method: string, url: string): string | null {
	const path = stripBase(url.split("?")[0] ?? "");
	const segments = splitPath(path);
	for (const route of FIXTURE_ROUTES) {
		if (route.method !== method.toUpperCase()) continue;
		const id = matchPattern(splitPath(route.pattern), segments);
		if (id === undefined) continue;
		if (id !== null && !/^[A-Za-z0-9_-]+$/.test(id)) return null;
		return id === null ? route.file : route.file.replace("{id}", id);
	}
	return null;
}

function stripBase(path: string): string {
	return path.startsWith(BENCH_API_BASE) ? path.slice(BENCH_API_BASE.length) : path;
}

function splitPath(path: string): string[] {
	return path.split("/").filter((segment) => segment.length > 0);
}

// Returns the ":id" segment (decoded), null when the pattern has none, undefined on no match.
function matchPattern(pattern: string[], segments: string[]): string | null | undefined {
	if (pattern.length !== segments.length) return undefined;
	let id: string | null = null;
	for (let i = 0; i < pattern.length; i++) {
		if (pattern[i] === ":id") {
			id = decodeURIComponent(segments[i]);
		} else if (pattern[i] !== segments[i]) {
			return undefined;
		}
	}
	return id;
}