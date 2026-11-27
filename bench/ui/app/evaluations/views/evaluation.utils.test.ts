import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { normalizeEvaluationList } from "@/lib/store/apis/evaluationsApi";
import i18n from "@/lib/i18n";
import {
	FAILURE_TEST_MODE,
	childRunsText,
	evaluationKind,
	evaluationResultText,
	evaluationSpend,
	benchmarkCount,
	childReasons,
	displayStatus,
	evaluationAgents,
	evaluationBenchmarks,
	evaluationBudget,
	filterEvaluations,
	parseEvaluationFilters,
	isActiveStatus,
	modeKey,
	sortNewestFirst,
	splitChildren,
	statusKey,
	statusTone,
} from "./evaluation.utils";

const ev = (over: Partial<Evaluation> = {}): Evaluation => ({
	id: "ev_1",
	spec: {
		name: "n",
		mode: "versions",
		arms: [
			{ label: "A", agent: "codex", version: 1, model: "m" },
			{ label: "B", agent: "codex", version: 2, model: "m" },
		],
		benchmarks: ["a", "b", "c"],
		tasks_per_benchmark: 5,
		trials: 2,
		faults: ["clean"],
	},
	status: "running",
	children: [],
	created_at: "2026-11-20T10:00:00Z",
	...over,
});

describe("evaluation utils", () => {
	it("maps modes and statuses to known keys only", () => {
		expect(modeKey("models")).toBe("mode.models");
		expect(modeKey("weird")).toBeNull();
		expect(statusKey("queued")).toBe("status.queued");
		expect(statusKey("x")).toBeNull();
	});

	it("polls only while moving", () => {
		expect(isActiveStatus("queued")).toBe(true);
		expect(isActiveStatus("running")).toBe(true);
		expect(isActiveStatus("done")).toBe(false);
		expect(isActiveStatus("cancelled")).toBe(false);
		expect(statusTone("failed")).toBe("bad");
		expect(statusTone("done")).toBe("ok");
	});

	it("lists agents with versions, deduped", () => {
		expect(evaluationAgents(ev())).toEqual(["codex v1", "codex v2"]);
		expect(
			evaluationAgents(
				ev({
					spec: {
						...ev().spec,
						arms: [
							{ label: "A", agent: "x", model: "m1" },
							{ label: "B", agent: "x", model: "m2" },
						],
					},
				}),
			),
		).toEqual(["x"]);
	});

	it("splits skipped children and counts benchmarks", () => {
		const children = [
			{ benchmark: "a", experiment_id: "e1", status: "running" },
			{ benchmark: "b", experiment_id: "", status: "skipped", skipped: "needs voice" },
		];
		const { run, skipped } = splitChildren(children);
		expect(run.map((c) => c.benchmark)).toEqual(["a"]);
		expect(skipped.map((c) => c.benchmark)).toEqual(["b"]);
		expect(benchmarkCount(ev())).toBe(3);
		expect(benchmarkCount(ev({ children }))).toBe(2);
	});

	it("sorts newest first and normalizes list shapes", () => {
		const list = sortNewestFirst([
			ev({ id: "old", created_at: "2026-01-01T00:00:00Z" }),
			ev({ id: "new", created_at: "2026-11-01T00:00:00Z" }),
		]);
		expect(list.map((e) => e.id)).toEqual(["new", "old"]);
		expect(normalizeEvaluationList([ev()]).length).toBe(1);
		expect(normalizeEvaluationList({ evaluations: [ev()] }).length).toBe(1);
		expect(normalizeEvaluationList(null)).toEqual([]);
	});
});
describe("evaluation utils (F08)", () => {
	it("gives each child that did not run its reason, skipped first, else its status", () => {
		expect(
			childReasons([
				{ benchmark: "bfcl", experiment_id: "", status: "skipped", skipped: "plan experiment: arm B is missing" },
				{ benchmark: "tau2", experiment_id: "e2", status: "failed" },
				{ benchmark: "x", experiment_id: "", status: "skipped" },
			]),
		).toEqual([
			{ benchmark: "bfcl", reason: "plan experiment: arm B is missing" },
			{ benchmark: "tau2", reason: "failed" },
			{ benchmark: "x", reason: "skipped" },
		]);
	});

	it("shows paused only for a running evaluation", () => {
		expect(displayStatus(ev({ status: "running", paused: true }))).toBe("paused");
		expect(displayStatus(ev({ status: "running" }))).toBe("running");
		expect(displayStatus(ev({ status: "done", paused: true }))).toBe("done");
	});

	it("sums the children's caps for the budget, else the spec's", () => {
		expect(
			evaluationBudget(
				ev({
					spec: { ...ev().spec, budget_usd: 1 },
					children: [
						{ benchmark: "a", experiment_id: "e1", status: "running", budget_usd: 1.1 },
						{ benchmark: "b", experiment_id: "", status: "skipped", budget_usd: 9 },
					],
				}),
			),
		).toBeCloseTo(1.1);
		expect(evaluationBudget(ev({ spec: { ...ev().spec, budget_usd: 3 } }))).toBe(3);
		expect(evaluationBudget(ev())).toBe(0);
	});

	it("lists the benchmarks from the children, else the spec", () => {
		expect(evaluationBenchmarks(ev())).toEqual(["a", "b", "c"]);
		expect(evaluationBenchmarks(ev({ children: [{ benchmark: "z", experiment_id: "e", status: "done" }] }))).toEqual(["z"]);
	});

	it("parses the URL filters and drops unknown values", () => {
		expect(parseEvaluationFilters({ q: " bfcl ", status: "done", mode: "score", sort: "spend" })).toEqual({
			q: "bfcl",
			status: "done",
			mode: "score",
			sort: "spend",
		});
		expect(parseEvaluationFilters({ status: "weird", sort: 3 })).toEqual({ q: "", status: "all", mode: "all", sort: "newest" });
	});

	it("searches name, id, agent and benchmark, filters status and mode, sorts", () => {
		const list = [
			ev({
				id: "ev_a",
				spec: { ...ev().spec, name: "Codex nightly", mode: "score" },
				status: "done",
				created_at: "2026-11-01T00:00:00Z",
				children: [{ benchmark: "bfcl", experiment_id: "e", status: "done", spent_usd: 2 }],
			}),
			ev({
				id: "ev_b",
				spec: { ...ev().spec, name: "alpha", arms: [{ label: "A", agent: "claude", model: "m" }] },
				status: "running",
				created_at: "2026-11-03T00:00:00Z",
				children: [{ benchmark: "tau2", experiment_id: "e", status: "running", spent_usd: 5 }],
			}),
			ev({ id: "ev_c", spec: { ...ev().spec, name: "beta" }, status: "queued", created_at: "2026-11-02T00:00:00Z" }),
		];
		const f = parseEvaluationFilters({});
		expect(filterEvaluations(list, f).map((e) => e.id)).toEqual(["ev_b", "ev_c", "ev_a"]);
		expect(filterEvaluations(list, { ...f, sort: "oldest" }).map((e) => e.id)).toEqual(["ev_a", "ev_c", "ev_b"]);
		expect(filterEvaluations(list, { ...f, sort: "name" }).map((e) => e.id)).toEqual(["ev_b", "ev_c", "ev_a"]);
		expect(filterEvaluations(list, { ...f, sort: "spend" }).map((e) => e.id)).toEqual(["ev_b", "ev_a", "ev_c"]);
		expect(filterEvaluations(list, { ...f, q: "BFCL" }).map((e) => e.id)).toEqual(["ev_a"]);
		expect(filterEvaluations(list, { ...f, q: "claude" }).map((e) => e.id)).toEqual(["ev_b"]);
		expect(filterEvaluations(list, { ...f, q: "ev_c" }).map((e) => e.id)).toEqual(["ev_c"]);
		expect(filterEvaluations(list, { ...f, status: "running" }).map((e) => e.id)).toEqual(["ev_b", "ev_c"]);
		expect(filterEvaluations(list, { ...f, status: "done" }).map((e) => e.id)).toEqual(["ev_a"]);
		expect(filterEvaluations(list, { ...f, mode: "score" }).map((e) => e.id)).toEqual(["ev_a"]);
	});
});
describe("stored result, kind and runs text (N161, N142)", () => {
	const t = i18n.getFixedT("en", "evaluations");
	const result = (scores: number[], cost?: number) => ({
		arms: scores.map((score, i) => ({ label: "AB"[i], agent: "codex", version: 1, model: "m", score, runs: 10 })),
		cost_usd: cost,
		at: "2026-11-23T00:00:00Z",
	});

	it("shows the stored score, one or two arms, and a dash without one", () => {
		expect(evaluationResultText(ev({ status: "done", result: result([0.56]) }))).toBe("56%");
		expect(evaluationResultText(ev({ status: "done", result: result([0.4, 0.45]) }))).toBe("A 40% · B 45%");
		expect(evaluationResultText(ev({ status: "running" }))).toBe("-");
	});

	it("takes spend from the result, else the children, else nothing", () => {
		expect(evaluationSpend(ev({ result: result([0.5], 1.25) }))).toBe(1.25);
		expect(evaluationSpend(ev({ children: [{ benchmark: "a", experiment_id: "e", status: "done", spent_usd: 0.5 }] }))).toBe(0.5);
		expect(evaluationSpend(ev())).toBeNull();
		// Children spend wins over the stored clean-runs result, like the evaluation page (N3-1).
		expect(evaluationSpend(ev({ result: result([0.5], 0.005), children: [{ benchmark: "a", experiment_id: "e", status: "done", spent_usd: 0.009 }] }))).toBe(0.009);
	});

	it("calls a score evaluation with faults a failure test", () => {
		expect(evaluationKind(ev({ spec: { ...ev().spec, mode: "score", faults: ["clean", "model-down"] } }))).toBe(FAILURE_TEST_MODE);
		expect(evaluationKind(ev({ spec: { ...ev().spec, mode: "score", faults: ["clean"] } }))).toBe("score");
		const list = [
			ev({ id: "ev_ft", spec: { ...ev().spec, mode: "score", faults: ["clean", "model-down"] } }),
			ev({ id: "ev_sc", spec: { ...ev().spec, mode: "score", faults: ["clean"] } }),
		];
		const f = parseEvaluationFilters({});
		expect(filterEvaluations(list, { ...f, mode: FAILURE_TEST_MODE }).map((e) => e.id)).toEqual(["ev_ft"]);
		expect(filterEvaluations(list, { ...f, mode: "score" }).map((e) => e.id)).toEqual(["ev_sc"]);
	});

	it("sorts by best result", () => {
		const list = [ev({ id: "ev_lo", result: result([0.2]) }), ev({ id: "ev_none" }), ev({ id: "ev_hi", result: result([0.1, 0.9]) })];
		expect(filterEvaluations(list, { ...parseEvaluationFilters({}), sort: "result" }).map((e) => e.id)).toEqual([
			"ev_hi",
			"ev_lo",
			"ev_none",
		]);
	});

	it("names the runs that could not run", () => {
		expect(childRunsText(t, { total: 30, done: 30, running: 0, failed: 0 }, false)).toBe("30 of 30 runs done");
		expect(childRunsText(t, { total: 30, done: 29, running: 0, failed: 1 }, false)).toBe(
			"29 of 30 runs scored, 1 infrastructure error",
		);
		expect(childRunsText(t, { total: 30, done: 20, running: 3, failed: 2 }, true)).toBe(
			"20 of 30 runs scored, 2 infrastructure errors, 3 running",
		);
		expect(childRunsText(t, { total: 30, done: 20, running: 3, failed: 0 }, true)).toBe("20 of 30 runs done, 3 running");
	});
});