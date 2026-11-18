import {
	RECEIPT_STEPS,
	type Grid,
	type PairResponse,
	type PlanResponse,
	type RunEventsResponse,
	type RunAgents,
	type RunReceiptsResponse,
	type RunResponse,
	type SuiteTaskResponse,
	type SuitesResponse,
	type VerdictResponse,
} from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { BENCH_API_BASE, FIXTURE_ROUTES, detectFixtureMode, resolveFixture } from "./fixtures";

const FIXTURE_DIR = path.resolve(__dirname, "../../../public/fixtures");
// The Go contract test reads the same key lists (bench/handlers/contract_test.go).
const CONTRACT_DIR = path.resolve(__dirname, "../../../../handlers/testdata/contract");
const readFixture = <T>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;
const fixtureFiles = fs.readdirSync(FIXTURE_DIR).filter((file) => file.endsWith(".json"));

describe("detectFixtureMode", () => {
	const cases = [
		{ name: "query flag", search: "?fixture=1", env: undefined, want: true },
		{ name: "query flag among others", search: "?exp=e_x&fixture=1", env: undefined, want: true },
		{ name: "env flag", search: "", env: "1", want: true },
		{ name: "neither", search: "?fixture=0", env: "0", want: false },
		{ name: "empty", search: "", env: undefined, want: false },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(detectFixtureMode(c.search, c.env)).toBe(c.want);
		});
	}
});

describe("resolveFixture", () => {
	const cases = [
		{ method: "GET", url: "/health", want: "health.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/catalog`, want: "catalog.json" },
		{ method: "POST", url: "/experiments/plan", want: "experiments.plan.json" },
		{ method: "POST", url: `${BENCH_API_BASE}/estimate`, want: "estimate.json" },
		{ method: "GET", url: "/experiments/e_x/matrix?condition=clean&grade_source=original", want: "experiments.matrix.json" },
		{ method: "POST", url: "/experiments", want: "experiments.create.json" },
		{ method: "post", url: "/experiments/e_x/start", want: "experiments.start.json" },
		{ method: "GET", url: "/experiments/e_x", want: "experiments.get.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/experiments`, want: "experiments.list.json" },
		{ method: "GET", url: "/experiments/e_x/verdict?x=1", want: "experiments.verdict.json" },
		{ method: "GET", url: "/runs/r_abc", want: "runs.r_abc.json" },
		{ method: "GET", url: "/runs/r_abc/events", want: "runs.r_abc.events.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/runs/r_abc/agents`, want: "runs.r_abc.agents.json" },
		{ method: "GET", url: "/pairs/p_abc", want: "pairs.p_abc.json" },
		{ method: "POST", url: "/audit", want: "audit.json" },
		{ method: "GET", url: "/leaderboard?suite=coding-core%401.0.0&scope=common", want: "leaderboard.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/trends?metric=tokens&agent=codex`, want: "trends.json" },
		{ method: "GET", url: "/suites", want: "suites.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/suites/coding-core/tasks/t01`, want: "suites.task.t01.json" },
		{ method: "GET", url: "/suites/..%2F/tasks/t01", want: null },
		{ method: "GET", url: "/suites/../tasks/t01", want: null },
		{ method: "GET", url: `${BENCH_API_BASE}/suites/acme-payments/health`, want: "suites.health.acme-payments.json" },
		// U3: a judged suite's calibration and labels; live scoring.
		{ method: "GET", url: "/suites/memory-recall/calibration", want: "suites.calibration.memory-recall.json" },
		{ method: "GET", url: "/suites/memory-recall/calibration/labels", want: "suites.calibration.labels.memory-recall.json" },
		{ method: "GET", url: "/livescore", want: "livescore.json" },
		{ method: "GET", url: "/livescore/sessions?limit=50", want: "livescore.sessions.json" },
		{ method: "GET", url: "/agents", want: "agents.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/agents/diff?a=sha256:abc&b=e_x:B`, want: "agents.diff.json" },
		{ method: "GET", url: "/agents/diff/versions", want: "agents.diff.versions.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/imports`, want: "imports.json" },
		{ method: "GET", url: "/workers", want: "workers.json" },
		{ method: "GET", url: "/experiments/e_x/reviews/queue?reason=veto", want: "experiments.reviews.queue.json" },
		{ method: "GET", url: "/experiments/e_x/reviews", want: "experiments.reviews.json" },
		{ method: "GET", url: "/runs/r_abc/reviews", want: "runs.r_abc.reviews.json" },
		{ method: "POST", url: "/reviews", want: "reviews.create.json" },
		{ method: "POST", url: "/experiments/e_x/regrade", want: "experiments.regrade.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/jobs`, want: "jobs.json" },
		{ method: "GET", url: "/jobs/job_abc", want: "jobs.job_abc.json" },
		{ method: "GET", url: "/jobs/job_abc/logs?since=4", want: "jobs.job_abc.logs.json" },
		{ method: "POST", url: "/regrade", want: null },
		{ method: "GET", url: "/experiments/plan/extra", want: null },
		{ method: "DELETE", url: "/health", want: null },
		{ method: "GET", url: "/runs/..%2F..%2Fsecret", want: null },
	];
	for (const c of cases) {
		it(`${c.method} ${c.url}`, () => {
			expect(resolveFixture(c.method, c.url)).toBe(c.want);
		});
	}
});

describe("fixture files", () => {
	it("cover every route", () => {
		for (const route of FIXTURE_ROUTES) {
			const [prefix, suffix] = route.file.split("{id}");
			const found = fixtureFiles.some((file) =>
				suffix === undefined ? file === prefix : file.startsWith(prefix) && file.endsWith(suffix),
			);
			expect(found, `${route.method} ${route.pattern} needs ${route.file}`).toBe(true);
		}
	});

	it("are all labelled synthetic", () => {
		for (const file of fixtureFiles) {
			expect(readFixture<{ mode: string }>(file).mode, file).toBe("synthetic");
		}
	});

	it("link the pair to run fixtures and grid cells", () => {
		const grid = readFixture<Grid>("experiments.grid.json");
		const pairFile = fixtureFiles.find((file) => file.startsWith("pairs."));
		expect(pairFile).toBeDefined();
		const pair = readFixture<PairResponse>(pairFile!);
		expect(pair.runs.map((run) => run.arm)).toEqual(["A", "B"]);
		for (const { run, slot } of pair.runs) {
			expect(slot.pair_id).toBe(pair.pair_id);
			const detail = readFixture<RunResponse>(`runs.${run.run_id}.json`);
			expect(detail.run.slot_id).toBe(slot.slot_id);
			for (const kind of ["events", "diff", "receipts"]) {
				expect(fixtureFiles).toContain(`runs.${run.run_id}.${kind}.json`);
			}
			const cell = grid.rows.flatMap((row) => row.cells).find((c) => c.run_id === run.run_id);
			expect(cell?.slot_id).toBe(slot.slot_id);
		}
	});

	it("link every suite task to its detail fixture", () => {
		const { suites } = readFixture<SuitesResponse>("suites.json");
		const tasks = suites.flatMap((suite) => suite.tasks.map((task) => ({ suite, task })));
		expect(tasks.length).toBeGreaterThan(0);
		for (const { suite, task } of tasks) {
			const detail = readFixture<SuiteTaskResponse>(`suites.task.${task.id}.json`);
			expect(detail.suite).toBe(suite.id);
			expect(detail.suite_version).toBe(suite.version);
			expect(detail.task).toEqual(task);
			expect(detail.files_total).toBe(detail.files.length);
		}
		for (const suite of suites) expect(suite.admitted_count).toBe(suite.tasks.filter((task) => task.admitted).length);
	});

	it("follow the Go contract for verdicts, events and receipts", () => {
		const { verdict } = readFixture<VerdictResponse>("experiments.verdict.json");
		expect(["improved", "regressed", "inconclusive", "invalid"]).toContain(verdict.decision);
		expect(verdict.effect?.per_task).toHaveLength(verdict.effect?.n_tasks ?? -1);
		for (const file of fixtureFiles.filter((f) => f.endsWith(".events.json"))) {
			for (const event of readFixture<RunEventsResponse>(file).events) {
				expect(event.event_id).toBe(`${event.run_id}:${event.source}:${event.seq}`);
			}
		}
		for (const file of fixtureFiles.filter((f) => f.endsWith(".receipts.json"))) {
			for (const receipt of readFixture<RunReceiptsResponse>(file).receipts) {
				expect(Object.keys(receipt.steps).sort()).toEqual([...RECEIPT_STEPS].sort());
			}
		}
	});
});

/** A contract key list: required keys, and optional ones (Go omitempty, "?" in the file). */
function contractKeys(type: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line.length > 0 && !line.startsWith("#"));
	return {
		required: lines.filter((line) => !line.endsWith("?")),
		optional: lines.filter((line) => line.endsWith("?")).map((line) => line.slice(0, -1)),
	};
}

type JsonObject = Record<string, unknown>;

/** The objects at a path of keys, where "[]" after a key means every element of that array. */
function objectsAt(root: JsonObject, keyPath: string): JsonObject[] {
	let current: JsonObject[] = [root];
	for (const segment of keyPath.split(".")) {
		const each = segment.endsWith("[]");
		const key = each ? segment.slice(0, -2) : segment;
		current = current.flatMap((obj) => {
			const value = obj[key];
			const items = each ? (Array.isArray(value) ? value : []) : [value];
			return items.filter((item): item is JsonObject => typeof item === "object" && item !== null && !Array.isArray(item));
		});
	}
	return current;
}

/** Fixture file -> its bench.ts type, plus nested objects to check: path -> type. */
function contractFor(file: string): { type: string; nested: Record<string, string> } | null {
	const byFile: Record<string, { type: string; nested: Record<string, string> }> = {
		"health.json": { type: "Health", nested: {} },
		"doctor.json": { type: "Doctor", nested: {} },
		"catalog.json": { type: "Catalog", nested: {} },
		"audit.json": { type: "Verdict", nested: {} },
		"experiments.regrade.json": { type: "Job", nested: {} },
		"jobs.json": { type: "JobList", nested: { "jobs[]": "Job" } },
		"experiments.plan.json": { type: "ExperimentPlan", nested: {} },
		"estimate.json": {
			type: "Forecast",
			nested: {
				"arms[]": "ArmForecast",
				"arms[].per_run": "PerRunRanges",
				"arms[].per_run.cost_usd": "ForecastRange",
				"arms[].per_run.tokens": "ForecastRange",
				"arms[].per_run.wall_s": "ForecastRange",
				"arms[].cost_usd": "ForecastRange",
				"arms[].tokens": "ForecastRange",
				"arms[].agent_time_s": "ForecastRange",
				cost_usd: "ForecastRange",
				agent_time_s: "ForecastRange",
				wall_clock_s: "ForecastRange",
				judge: "JudgeForecast",
				"judge.cost_usd": "ForecastRange",
			},
		},
		"experiments.matrix.json": {
			type: "ModelMatrix",
			nested: { "rows[]": "MatrixRow", "pairs[]": "MatrixPair", "excluded[]": "LeaderboardExclusion" },
		},
		"experiments.create.json": { type: "ExperimentCreated", nested: {} },
		"experiments.get.json": { type: "ExperimentStatus", nested: { slots: "SlotProgress" } },
		"experiments.start.json": { type: "ExperimentStatus", nested: { slots: "SlotProgress" } },
		"experiments.list.json": { type: "ExperimentList", nested: { "experiments[]": "ExperimentStatus" } },
		"experiments.verdict.json": { type: "VerdictPayload", nested: { verdict: "Verdict" } },
		"experiments.diagnostics.json": { type: "DiagnosticsPayload", nested: {} },
		"experiments.grid.json": { type: "Grid", nested: { "rows[]": "GridRow", "rows[].cells[]": "GridCell" } },
		"releases.list.json": { type: "ReleaseList", nested: { "releases[]": "ReleaseRecord" } },
		"experiments.triage.json": { type: "Triage", nested: { "groups[]": "TriageGroup" } },
		"clusters.json": {
			type: "ClusterList",
			nested: {
				filters: "ClusterFilters",
				"clusters[]": "ClusterSummary",
				"clusters[].layers[]": "ClusterLayer",
				"clusters[].related[]": "ClusterRelated",
				"clusters[].after_config_change": "ClusterConfigChange",
				"excluded[]": "ClusterExclusion",
			},
		},
		"leaderboard.json": { type: "Leaderboard", nested: { "entries[]": "LeaderboardEntry", "excluded[]": "LeaderboardExclusion" } },
		"trends.json": {
			type: "Trends",
			nested: {
				"lineages[]": "TrendLineage",
				"lineages[].points[]": "TrendPoint",
				"lineages[].points[].regression": "TrendRegression",
				"lineages[].regression": "TrendRegression",
			},
		},
		"imports.json": { type: "ImportList", nested: { "importers[]": "ImporterInfo" } },
		"workers.json": { type: "WorkerList", nested: { "workers[]": "WorkerInfo" } },
		"agents.diff.versions.json": {
			type: "AgentDiffVersionList",
			nested: { "versions[]": "AgentDiffVersionInfo", "versions[].arms[]": "AgentDiffArmRef" },
		},
		"agents.diff.json": {
			type: "AgentDiffReport",
			nested: {
				a: "AgentDiffVersion",
				b: "AgentDiffVersion",
				"a.excluded[]": "AgentDiffExclusion",
				config: "AgentDiffConfig",
				"config.groups[]": "AgentDiffConfigGroup",
				"config.groups[].fields[]": "AgentDiffFieldChange",
				"config.groups[].files[]": "AgentDiffFileChange",
				outcome: "AgentDiffOutcome",
				"outcome.counts": "AgentDiffClassCounts",
				"outcome.test": "AgentDiffSignTest",
				"outcome.tasks[]": "AgentDiffTask",
				"outcome.tasks[].pair": "AgentDiffRunPair",
				"outcome.tasks[].pair.a": "AgentDiffRunRef",
				"outcome.tasks[].pair.b": "AgentDiffRunRef",
				"outcome.metrics[]": "AgentDiffMetric",
				"outcome.gates[]": "AgentDiffGate",
			},
		},
		"experiments.reviews.queue.json": { type: "ReviewQueue", nested: { "items[]": "ReviewQueueItem" } },
		"experiments.reviews.json": { type: "ReviewLog", nested: { "reviews[]": "Review" } },
		"reviews.create.json": { type: "Review", nested: {} },
	};
	if (byFile[file]) return byFile[file];
	// Failure inbox area: the list, then one item's detail per id.
	if (file === "inbox.json" || file.startsWith("inbox.")) {
		const item = file === "inbox.json" ? "items[]" : "item";
		return {
			type: file === "inbox.json" ? "InboxList" : "InboxItemDetail",
			nested: {
				...(file === "inbox.json"
					? {
							"sources[]": "InboxSourceStatus",
							"suites[]": "InboxSuite",
							"items[].task": "InboxTaskLink",
							"items[].release": "InboxReleaseStatus",
						}
					: {}),
				[item]: "InboxItem",
				[`${item}.signature_parts`]: "InboxSignatureParts",
				[`${item}.traces[]`]: "InboxTraceRef",
			},
		};
	}
	// W5 drift canary: the list, then one canary's detail per name.
	// W3 customer support: one experiment's support scorecard.
	if (file.startsWith("experiments.support.")) {
		return { type: "SupportSummary", nested: { "arms[]": "SupportArm" } };
	}
	if (file === "canaries.json") {
		return {
			type: "CanaryList",
			nested: {
				"canaries[]": "CanarySummary",
				"canaries[].targets[]": "CanaryTarget",
				"canaries[].baseline": "CanaryBaseline",
				"canaries[].baseline.served[]": "CanaryServed",
				"canaries[].last_look": "CanaryRecord",
				"errors[]": "CanaryDefinitionLoad",
			},
		};
	}
	if (file.startsWith("canaries.")) {
		return {
			type: "CanaryDetail",
			nested: {
				summary: "CanarySummary",
				definition: "CanaryDefinition",
				"definition.budget": "CanaryBudget",
				"baselines[]": "CanaryBaseline",
				"records[]": "CanaryRecord",
				"records[].comparison": "CanaryComparison",
				"records[].comparison.tests[]": "CanaryMetricTest",
				"records[].changes": "CanaryChanges",
				"records[].changes.arms[]": "CanaryArmChanges",
				// Only the nightly fixture watches one model through two providers.
				...(file === "canaries.nightly.json" ? { "records[].equivalence[]": "CanaryEquivalence", "pairs[]": "CanaryEquivalencePair" } : {}),
				"records[].served[]": "CanaryServed",
				"records[].points[]": "CanaryArmPoint",
				"history[]": "TrendLineage",
				"history[].points[]": "TrendPoint",
			},
		};
	}
	if (file === "suites.json") {
		return {
			type: "SuiteList",
			nested: {
				"suites[]": "SuiteSummary",
				"suites[].base_image": "SuiteImage",
				"suites[].drift[]": "SuiteDrift",
				"suites[].fingerprint_drift[]": "FingerprintDrift",
				"suites[].tiers[]": "SuiteTier", // U3
				"suites[].probes[]": "SuiteProbe",
				"suites[].tasks[]": "SuiteTask",
				"suites[].tasks[].checks[]": "SuiteAdmissionCheck",
				"suites[].tasks[].provenance": "TaskProvenance",
				"suites[].imported": "ImportRecord",
			},
		};
	}
	// U3: judge calibration and live scoring in fixture mode.
	if (file.startsWith("suites.calibration.labels.")) return { type: "CalibrationLabels", nested: { "items[]": "CalibrationItem" } };
	if (file.startsWith("suites.calibration.")) return { type: "CalibrationStatus", nested: {} };
	if (file === "livescore.json") return { type: "LiveStatus", nested: {} };
	if (file === "livescore.compare.json") return { type: "LiveComparison", nested: {} };
	if (file === "livescore.sessions.json") return { type: "LiveSessionList", nested: { "sessions[]": "LiveSessionRecord" } };
	if (file.startsWith("suites.task.")) return { type: "SuiteTaskDetail", nested: { task: "SuiteTask", "files[]": "SuiteRepoFile" } };
	if (file.startsWith("suites.health.")) {
		return {
			type: "SuiteHealth",
			nested: {
				thresholds: "HealthThresholds",
				"tasks[]": "TaskHealth",
				"tasks[].quarantine": "TaskQuarantine",
				"tasks[].verifier": "VerifierHealth",
				"tasks[].verifier.evidence[]": "VerifierEvidence",
				"tasks[].variance": "OutcomeVariance",
				"tasks[].variance.per_config[]": "ConfigOutcome",
				"tasks[].infra": "InfraHealth",
				"tasks[].difficulty": "DifficultyHealth",
			},
		};
	}
	if (file === "agents.json") {
		return {
			type: "AgentList",
			nested: { "agents[]": "AgentSummary", "agents[].budgets": "AgentBudgets", "agents[].gateway": "AgentGateway" },
		};
	}
	if (file.startsWith("clusters.")) {
		return {
			type: "ClusterDetail",
			nested: { filters: "ClusterFilters", cluster: "ClusterSummary", "cluster.layers[]": "ClusterLayer", "runs[]": "ClusterRun" },
		};
	}
	if (file.startsWith("pairs.")) return { type: "Pair", nested: { "runs[]": "PairRun" } };
	if (file.startsWith("jobs.") && file.endsWith(".logs.json")) return { type: "JobLogs", nested: { "lines[]": "JobLogLine" } };
	if (file.startsWith("jobs.")) return { type: "Job", nested: {} };
	if (!file.startsWith("runs.")) return null;
	if (file.endsWith(".events.json")) return { type: "RunEvents", nested: {} };
	if (file.endsWith(".diff.json")) return { type: "RunDiff", nested: {} };
	if (file.endsWith(".receipts.json")) return { type: "RunReceipts", nested: {} };
	if (file.endsWith(".steps.json")) return { type: "RunSteps", nested: { "steps[]": "StepRecord", "observers[]": "ObserverStatus" } };
	// A single-agent run (available false) has empty lists and no tree or blame, so nothing nested to check.
	if (file.endsWith(".agents.json")) return { type: "RunAgents", nested: readFixture<RunAgents>(file).available ? RUN_AGENTS_NESTED : {} };
	if (file.endsWith(".reviews.json")) return { type: "RunReviews", nested: { "reviews[]": "Review" } };
	return { type: "RunDetail", nested: {} };
}

/** Nested objects of a multi-agent RunAgents fixture: path -> contract type. */
const RUN_AGENTS_NESTED: Record<string, string> = {
	tree: "AgentTree",
	"tree.agents[]": "AgentNode",
	"tree.edges[]": "AgentEdge",
	"tree.coverage": "AgentCoverage",
	"members[]": "AgentStats",
	"spans[]": "AgentSpan",
	"handoffs[]": "AgentHandoff",
	"handoffs[].facts[]": "HandoffFact",
	blame: "Blame",
	"blame.candidates[]": "Candidate",
	"blame.replay": "Replay",
	"blame.coverage": "AgentCoverage",
};

/** Problems with one object's keys against a contract key list. "_comment" labels a fixture and is allowed. */
function keyProblems(where: string, obj: JsonObject, type: string, topLevel: boolean): string[] {
	const { required, optional } = contractKeys(type);
	const need = topLevel && !required.includes("mode") ? [...required, "mode"] : required;
	const allowed = new Set([...need, ...optional, ...(topLevel ? ["_comment"] : [])]);
	return [
		...need.filter((key) => !(key in obj)).map((key) => `${where}: missing ${key}`),
		...Object.keys(obj)
			.filter((key) => !allowed.has(key))
			.map((key) => `${where}: ${key} is not in ${type}`),
	];
}

describe("fixture shapes", () => {
	it("match the Go contract key lists", () => {
		const problems: string[] = [];
		for (const file of fixtureFiles) {
			const contract = contractFor(file);
			expect(contract, `${file} has no contract type`).not.toBeNull();
			const body = readFixture<JsonObject>(file);
			problems.push(...keyProblems(file, body, contract!.type, true));
			for (const [keyPath, type] of Object.entries(contract!.nested)) {
				const objects = objectsAt(body, keyPath);
				expect(objects.length, `${file} ${keyPath}`).toBeGreaterThan(0);
				objects.forEach((obj, i) => problems.push(...keyProblems(`${file} ${keyPath} #${i}`, obj, type, false)));
			}
		}
		expect(problems).toEqual([]);
	});

	it("leave plan estimates unknown until a pilot exists", () => {
		const plan = readFixture<PlanResponse>("experiments.plan.json");
		for (const estimate of [plan.est_wall_s, plan.est_usd]) {
			expect(estimate).toMatchObject({ state: "unknown", v: null, reason: "no pilot yet" });
		}
	});
});