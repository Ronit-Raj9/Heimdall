import { describe, expect, it } from "vitest";
import { costEstimate, defaultAgent, draftRequest, missingFields, suitePickable } from "./featureTestDialog";
import type { ExperimentPlan, Forecast } from "@/lib/types/bench";

const form = { suite: "s@1", base: "agents/mcploop.yaml", model: "", provider: "", budget: "1", trials: "1", taskCount: "3", name: " zz-test ", options: {} };
const plan = { errors: [], est_usd: { state: "unknown", v: null }, est_wall_s: { state: "unknown", v: null } } as unknown as ExperimentPlan;

describe("feature test form, round 4", () => {
	it("sends task_count and name the way the server reads them", () => {
		const { req } = draftRequest("gateway_overhead", form);
		expect(req?.task_count).toBe(3);
		expect(req?.name).toBe("zz-test");
		expect(missingFields("gateway_overhead", { ...form, taskCount: "0" })).toContain("taskCount");
		expect(draftRequest("gateway_overhead", { ...form, taskCount: "" }).req?.task_count).toBeUndefined();
	});
	it("lets a part-ready benchmark be picked, not one with no ready task", () => {
		expect(suitePickable({ task_count: 3641, admitted_count: 36 })).toBe(true);
		expect(suitePickable({ task_count: 5, admitted_count: 0 })).toBe(false);
	});
	it("starts with a tool-using agent, never bare-model", () => {
		const agents = [{ id: "bare-model", path: "agents/bare-model.yaml" }, { id: "codex", path: "agents/codex.yaml" }];
		expect(defaultAgent(agents)?.id).toBe("codex");
		expect(defaultAgent([agents[0]])).toBeUndefined();
	});
	it("names the forecast when the plan has no estimate", () => {
		const forecast = { cost_usd: { kind: "history", low: 0.01, median: 0.02, high: 0.05 } } as unknown as Forecast;
		const draft = { experiment: {} as never, notes: [] };
		expect(costEstimate({ draft, plan, forecast })).toEqual({ usd: 0.02, high: 0.05, upper: false });
		expect(costEstimate({ draft, plan })).toBeNull();
	});
});
