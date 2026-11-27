import { describe, expect, it } from "vitest";
import { benchmarkReadiness, setupSteps } from "./home.utils";

const state = (steps: ReturnType<typeof setupSteps>) => steps.map((s) => `${s.id}:${s.done ? "done" : (s.blockedBy ?? "next")}`);

describe("Home set-up steps (Q-301, Q-302)", () => {
	it("no key: the key is next, every later step is locked behind it", () => {
		expect(state(setupSteps({ keys: false, agent: false, benchmark: false, evaluation: false, finished: false }))).toEqual([
			"keys:next",
			"agent:keys",
			"benchmark:keys",
			"evaluate:keys",
			"report:keys",
		]);
	});
	it("key only: the agent is next", () => {
		expect(state(setupSteps({ keys: true, agent: false, benchmark: true, evaluation: false, finished: false }))).toEqual([
			"keys:done",
			"agent:next",
			"benchmark:done",
			"evaluate:agent",
			"report:agent",
		]);
	});
	it("key and agent, no ready benchmark: the benchmark is next, not the evaluation", () => {
		const steps = setupSteps({ keys: true, agent: true, benchmark: false, preparing: 2, evaluation: false, finished: false });
		expect(state(steps)).toEqual(["keys:done", "agent:done", "benchmark:next", "evaluate:benchmark", "report:benchmark"]);
		expect(steps[2]).toMatchObject({ to: "/suites", preparing: 2 });
	});
	it("counts ready and preparing benchmarks", () => {
		expect(benchmarkReadiness([{ admitted_count: 3 }, { admitted_count: 0 }, {}])).toEqual({ ready: 1, preparing: 2 });
	});
});
