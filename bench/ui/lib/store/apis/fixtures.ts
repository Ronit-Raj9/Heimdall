// Fixture mode: with ?fixture=1 or VITE_BENCH_FIXTURE=1, RTK Query reads public/fixtures/*.json
// instead of calling the server, so screens can be built with no bench server running.
// Everything here is pure so it can be tested without a browser.

/** Prefix of every bench API route (DESIGN.md 16.7). */
export const BENCH_API_BASE = "/api/bench";

/** One API route and the fixture file that answers it. `{id}` in the file is the path id. */
export interface FixtureRoute {
	method: "GET" | "POST";
	/** Path below BENCH_API_BASE; ":id" matches one path segment, "*" any one segment (not used in the file name). */
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
	{ method: "POST", pattern: "/estimate", file: "estimate.json" },
	{ method: "POST", pattern: "/experiments", file: "experiments.create.json" },
	{ method: "POST", pattern: "/experiments/:id/start", file: "experiments.start.json" },
	{ method: "GET", pattern: "/experiments", file: "experiments.list.json" },
	{ method: "GET", pattern: "/experiments/:id", file: "experiments.get.json" },
	{ method: "GET", pattern: "/experiments/:id/verdict", file: "experiments.verdict.json" },
	{ method: "GET", pattern: "/experiments/:id/diagnostics", file: "experiments.diagnostics.json" },
	{ method: "GET", pattern: "/experiments/:id/grid", file: "experiments.grid.json" },
	{ method: "GET", pattern: "/experiments/:id/triage", file: "experiments.triage.json" },
	// Failure clusters: one list for every filter; one cluster's detail per signature, others answer 404.
	{ method: "GET", pattern: "/clusters", file: "clusters.json" },
	{ method: "GET", pattern: "/clusters/:id", file: "clusters.{id}.json" },
	// Human review: the queue, the review log, one run's reviews and the created review.
	{ method: "GET", pattern: "/experiments/:id/reviews/queue", file: "experiments.reviews.queue.json" },
	{ method: "GET", pattern: "/experiments/:id/reviews", file: "experiments.reviews.json" },
	{ method: "GET", pattern: "/runs/:id/reviews", file: "runs.{id}.reviews.json" },
	{ method: "POST", pattern: "/reviews", file: "reviews.create.json" },
	{ method: "GET", pattern: "/experiments/:id/matrix", file: "experiments.matrix.json" },
	// W3 customer support: the scorecard of one support experiment; other ids answer 404 and the card stays hidden.
	{ method: "GET", pattern: "/experiments/:id/support", file: "experiments.support.{id}.json" },
	{ method: "GET", pattern: "/runs/:id", file: "runs.{id}.json" },
	{ method: "GET", pattern: "/runs/:id/events", file: "runs.{id}.events.json" },
	{ method: "GET", pattern: "/runs/:id/diff", file: "runs.{id}.diff.json" },
	{ method: "GET", pattern: "/runs/:id/receipts", file: "runs.{id}.receipts.json" },
	{ method: "GET", pattern: "/runs/:id/steps", file: "runs.{id}.steps.json" }, // Agent step timeline (stepTimeline.tsx)
	{ method: "GET", pattern: "/pairs/:id", file: "pairs.{id}.json" },
	{ method: "POST", pattern: "/audit", file: "audit.json" },
	// A regrade runs as a job: the route answers with the queued experiment.regate job.
	{ method: "POST", pattern: "/experiments/:id/regrade", file: "experiments.regrade.json" },
	{ method: "GET", pattern: "/releases", file: "releases.list.json" },
	// One board for every suite, condition, scope and synthetic filter: the query string is ignored.
	{ method: "GET", pattern: "/leaderboard", file: "leaderboard.json" },
	{ method: "GET", pattern: "/trends", file: "trends.json" }, // Index and trends area: one response for every filter
	{ method: "GET", pattern: "/suites", file: "suites.json" },
	// Task ids are unique across the fixture suites, so the file is keyed by the task alone.
	{ method: "GET", pattern: "/suites/*/tasks/:id", file: "suites.task.{id}.json" },
	// Suite health per suite id; a suite with no file answers 404, which the page shows as health unavailable.
	{ method: "GET", pattern: "/suites/:id/health", file: "suites.health.{id}.json" },
	{ method: "GET", pattern: "/agents", file: "agents.json" },
	// Agent diff: one report for every a, b, suite and condition (the query string is ignored).
	{ method: "GET", pattern: "/agents/diff", file: "agents.diff.json" },
	{ method: "GET", pattern: "/agents/diff/versions", file: "agents.diff.versions.json" },
	{ method: "GET", pattern: "/imports", file: "imports.json" },
	// Workers on: the page shows the table. A server with the worker API off answers 404 instead.
	{ method: "GET", pattern: "/workers", file: "workers.json" },
	// Jobs (hosted platform, also local): the drawer's list, one job and its log (poll form).
	{ method: "GET", pattern: "/jobs", file: "jobs.json" },
	{ method: "GET", pattern: "/jobs/:id", file: "jobs.{id}.json" },
	{ method: "GET", pattern: "/jobs/:id/logs", file: "jobs.{id}.logs.json" },
	// Failure inbox area: one list for every state and source; two items have a detail, others answer 404.
	{ method: "GET", pattern: "/inbox", file: "inbox.json" },
	{ method: "GET", pattern: "/inbox/:id", file: "inbox.{id}.json" },
	// W5 drift canary: the list, then one canary's detail per name; other names answer 404.
	{ method: "GET", pattern: "/canaries", file: "canaries.json" },
	{ method: "GET", pattern: "/canaries/:id", file: "canaries.{id}.json" },
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
// A "*" segment matches any one safe segment and is not returned.
function matchPattern(pattern: string[], segments: string[]): string | null | undefined {
	if (pattern.length !== segments.length) return undefined;
	let id: string | null = null;
	for (let i = 0; i < pattern.length; i++) {
		if (pattern[i] === ":id") {
			id = decodeURIComponent(segments[i]);
		} else if (pattern[i] === "*") {
			const segment = decodeURIComponent(segments[i]);
			if (!/^[A-Za-z0-9_.-]+$/.test(segment) || /^\.+$/.test(segment)) return undefined;
		} else if (pattern[i] !== segments[i]) {
			return undefined;
		}
	}
	return id;
}