import "@/lib/i18n";

import { agentCardStats, ownAgents } from "@/components/home/yourAgents";
import { groupsForReport, reportExperiments } from "@/components/reportcard/reportFailures";
import { emptyReport, unwrapAgentReportCard, type Evaluation, type Report } from "@/lib/store/apis/evaluationsApi";
import { normalizeFailurePatterns, patternRate, seenPatterns, type FailurePatternGroup } from "@/lib/store/apis/failurePatternsApi";
import type { AgentConfigFields } from "@/lib/types/agentBuilder";
import type { AgentSummary, RunListRow } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { failureTestSpec, formProblem, pastFailureTests, type FailureTestForm } from "../../../failure-tests/views/failureTestsView";
import { groupName, patternIds } from "../../../why-agents-fail/views/whyAgentsFailView";
import { CATEGORIES } from "../../new/views/newAgentView";
import { latestVersion } from "./agentDetailView";
import { runCost, visibleAgentRuns } from "./agentRunsTab";
import { buildFields, parseList, textsOf } from "./configTab";

const report = (over: Partial<Report> = {}): Report => ({
	evaluation: "ev_1",
	mode: "score",
	arms: [{ label: "A", agent: "support-bot", version: 2, model: "gpt-x" }],
	capabilities: [{ capability: "support", benchmarks: ["b1", "b2"], arms: { A: { score: 0.5, ci_low: 0.4, ci_high: 0.6, runs: 20 } } }],
	benchmarks: [
		{
			benchmark: "b1",
			capability: "support",
			experiment_id: "e1",
			arms: { A: { score: 0.5, ci_low: 0, ci_high: 1, runs: 10, cost_usd: 0.2 } },
		},
		{
			benchmark: "b2",
			capability: "support",
			experiment_id: "e2",
			arms: { A: { score: 0.25, ci_low: 0, ci_high: 1, runs: 4, cost_usd: 0.1 } },
		},
	],
	reliability: [
		{ fault: "broken-tool", arms: { A: { score: 0.3, drop: -0.2 } } },
		{ fault: "model-down", arms: { A: { score: 0.1, drop: -0.4 } } },
	],
	...over,
});

const group = (over: Partial<FailurePatternGroup> = {}): FailurePatternGroup => ({
	key: "v2",
	label: "v2",
	agent: "support-bot",
	version: 2,
	models: [],
	runs: 10,
	failed: 3,
	experiments: [],
	last_at: "",
	patterns: [
		{ pattern: "pattern_loop", label: "Loop", description: "", count: 1, observed: 10, unknown: 0, examples: [] },
		{ pattern: "pattern_unverified_finish", label: "Unverified finish", description: "", count: 4, observed: 8, unknown: 2, examples: [] },
		{ pattern: "pattern_ignored_error", label: "Ignored error", description: "", count: 0, observed: 0, unknown: 10, examples: [] },
	],
	...over,
});

describe("agent report card response", () => {
	it("unwraps the {agent, report} wrapper; no report is an empty card", () => {
		expect(unwrapAgentReportCard({ agent: "x", report: report() }).evaluation).toBe("ev_1");
		expect(unwrapAgentReportCard({ agent: "x", report: null })).toEqual(emptyReport());
		expect(unwrapAgentReportCard(report()).evaluation).toBe("ev_1");
		expect(unwrapAgentReportCard(null)).toEqual(emptyReport());
	});
});

describe("agent page", () => {
	it("latest version", () => {
		expect(latestVersion([])).toBeNull();
		expect(latestVersion(undefined)).toBeNull();
		expect(latestVersion([{ version: 1 }, { version: 3 }, { version: 2 }])).toBe(3);
	});

	it("runs tab filters failures and reads measured cost only", () => {
		const rows = [
			{ run_id: "a", outcome: "pass", cost_usd: { state: "observed", v: 0.5 } },
			{ run_id: "b", outcome: "fail", cost_usd: { state: "unknown", v: null } },
			{ run_id: "c", outcome: "timeout" },
		] as unknown as RunListRow[];
		expect(visibleAgentRuns(rows, true).map((r) => r.run_id)).toEqual(["b", "c"]);
		expect(visibleAgentRuns(rows, false)).toHaveLength(3);
		expect(rows.map(runCost)).toEqual([0.5, null, null]);
	});

	it("setup form round-trips behavior text boxes without a spurious change", () => {
		const fields: AgentConfigFields = {
			id: "support-bot",
			adapter: "command",
			model: "gpt-x",
			provider: "openai",
			wall_s: 600,
			cpus: 2,
			mem_gb: 4,
			turns: 0,
			behavior: {
				notes_project_file: "AGENTS.md",
				notes_user_file: "",
				notes_user_path: "",
				memory_paths: ["/a", "/b"],
				max_retries: 2,
				max_tool_calls: 0,
				provider_retries: 0,
				fallbacks: ["m1", "m2"],
				capabilities: ["shell"],
			},
		};
		expect(JSON.stringify(buildFields(fields, textsOf(fields)))).toBe(
			JSON.stringify({ ...fields, env: {}, behavior: { ...fields.behavior } }),
		);
		const edited = buildFields(fields, { ...textsOf(fields), memory: "/a\n\n /c ", fallbacks: "m3,  m4" });
		expect(edited.behavior?.memory_paths).toEqual(["/a", "/c"]);
		expect(edited.behavior?.fallbacks).toEqual(["m3", "m4"]);
		// An older server sent no behavior: none goes back.
		const { behavior: _b, ...old } = fields;
		expect(buildFields(old, textsOf(old)).behavior).toBeUndefined();
		expect(parseList(" a, b\nc ,,")).toEqual(["a", "b", "c"]);
	});

	it("new agent gallery puts bring your own first", () => {
		expect(CATEGORIES[0]).toBe("bring-your-own");
		expect(CATEGORIES).toHaveLength(4);
	});
});

describe("home agent cards", () => {
	it("score per capability, cost per solved task and worst fault drop", () => {
		const s = agentCardStats(report());
		expect(s.capabilities).toEqual([{ id: "support", score: 0.5 }]);
		// cost (0.2*10 + 0.1*4 = 2.4) over solved runs (0.5*10 + 0.25*4 = 6)
		expect(s.costPerSolved).toBeCloseTo(0.4);
		// drop is the change from clean: negative means the fault hurt (fix wave 2, N126).
		expect(s.worstDrop).toEqual({ fault: "model-down", drop: -0.4 });
		expect(agentCardStats(undefined)).toEqual({ capabilities: [], costPerSolved: null, worstDrop: null, faultsTested: false });
		const unpriced = report({
			benchmarks: [
				{
					benchmark: "b1",
					capability: "support",
					experiment_id: "e1",
					arms: { A: { score: 0.5, ci_low: 0, ci_high: 1, runs: 2, cost_usd: 0 } },
				},
			],
		});
		expect(agentCardStats(unpriced).costPerSolved).toBeNull();
	});

	it("lists only the org's own agents", () => {
		const a = (id: string, extra: Partial<AgentSummary> = {}) => ({ id, adapter: "command", scripted_kind: "", ...extra }) as AgentSummary;
		expect(
			ownAgents([
				a("codex"),
				a("support-bot"),
				a("oracle", { adapter: "scripted" }),
				a("broken", { error: "bad" } as never),
				a("mine"),
			]).map((x) => x.id),
		).toEqual(["support-bot", "mine"]);
	});
});

describe("failure patterns", () => {
	it("normalizes nulls, sorts seen patterns and computes rates", () => {
		const n = normalizeFailurePatterns({
			groups: [{ ...group(), models: null, patterns: [{ ...group().patterns[0], examples: null }] } as never],
		});
		expect(n.groups[0].models).toEqual([]);
		expect(n.groups[0].patterns[0].examples).toEqual([]);
		expect(normalizeFailurePatterns(null).groups).toEqual([]);
		expect(seenPatterns(group()).map((p) => p.pattern)).toEqual(["pattern_unverified_finish", "pattern_loop"]);
		expect(patternRate({ count: 4, observed: 8 })).toBe(0.5);
		expect(patternRate({ count: 0, observed: 0 })).toBeNull();
	});

	it("report card failures read the report's experiments and arms", () => {
		expect(reportExperiments(report())).toEqual(["e1", "e2"]);
		expect(groupsForReport([group({ key: "A" }), group({ key: "B" })], report()).map((g) => g.key)).toEqual(["A"]);
		expect(groupsForReport([group({ key: "A" })], { arms: [] })).toHaveLength(1);
	});

	it("why agents fail names versions and lists every pattern once", () => {
		expect(groupName({ version: 3, label: "v3" }, "Unsaved config")).toBe("v3");
		expect(groupName({ version: 0, label: "unsaved config abc123" }, "Unsaved config")).toBe("Unsaved config abc123");
		expect(patternIds([group(), group({ key: "v1" })]).map((p) => p.pattern)).toEqual([
			"pattern_loop",
			"pattern_unverified_finish",
			"pattern_ignored_error",
		]);
	});
});

describe("failure tests", () => {
	const form: FailureTestForm = {
		agent: "support-bot",
		version: 2,
		provider: "openai",
		model: "gpt-x",
		benchmarks: ["b1"],
		faults: ["broken-tool", "model-down"],
		tasks: 3,
		trials: 1,
	};

	it("launches one score arm with a clean baseline plus the chosen faults in the order picked", () => {
		const spec = failureTestSpec(form);
		expect(spec.mode).toBe("score");
		expect(spec.arms).toEqual([{ label: "A", agent: "support-bot", version: 2, model: "gpt-x", provider: "openai" }]);
		expect(spec.faults).toEqual(["clean", "broken-tool", "model-down"]);
		expect(spec.name).toBe("Failure test: support-bot v2");
		expect(failureTestSpec({ ...form, version: 0, provider: "" }).arms[0]).toEqual({ label: "A", agent: "support-bot", model: "gpt-x" });
	});

	it("says what is missing before launch", () => {
		expect(formProblem(form)).toBeNull();
		expect(formProblem({ ...form, agent: "" })).toBe("agent");
		expect(formProblem({ ...form, model: "" })).toBe("model");
		expect(formProblem({ ...form, benchmarks: [] })).toBe("benchmarks");
		expect(formProblem({ ...form, faults: [] })).toBe("faults");
	});

	it("past tests are evaluations with a fault, newest first, for the agent", () => {
		const ev = (id: string, agent: string, faults: string[], at: string) =>
			({
				id,
				status: "done",
				created_at: at,
				children: [],
				spec: {
					name: id,
					mode: "score",
					arms: [{ label: "A", agent, model: "m" }],
					benchmarks: [],
					tasks_per_benchmark: 1,
					trials: 1,
					faults,
				},
			}) as Evaluation;
		const list = [
			ev("e1", "support-bot", ["clean", "broken-tool"], "2026-11-01T00:00:00Z"),
			ev("e2", "support-bot", ["clean"], "2026-11-02T00:00:00Z"),
			ev("e3", "agents/support-bot.yaml@v2", ["clean", "model-down"], "2026-11-03T00:00:00Z"),
			ev("e4", "codex", ["model-down"], "2026-11-04T00:00:00Z"),
		];
		expect(pastFailureTests(list, "support-bot").map((e) => e.id)).toEqual(["e3", "e1"]);
		expect(pastFailureTests(list, "").map((e) => e.id)).toEqual(["e4", "e3", "e1"]);
	});
});