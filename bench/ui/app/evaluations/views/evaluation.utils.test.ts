import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { normalizeEvaluationList } from "@/lib/store/apis/evaluationsApi";
import {
	benchmarkCount,
	evaluationAgents,
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