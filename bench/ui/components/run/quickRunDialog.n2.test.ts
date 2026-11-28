import { describe, expect, it } from "vitest";
import { QUICKRUN_DEFAULT_BUDGET, agentModelRef, quickRunProblems } from "./quickRunDialog";

describe("quick test defaults (N2)", () => {
	it("takes the agent's own model from its config", () => {
		expect(agentModelRef("openai/gpt-6-luna")).toEqual({ provider: "openai", model: "gpt-6-luna" });
		expect(agentModelRef("openrouter/meta/llama-4")).toEqual({ provider: "openrouter", model: "meta/llama-4" });
		expect(agentModelRef("")).toBeNull();
		expect(agentModelRef("gpt-6-luna")).toBeNull();
	});
	it("starts at a one dollar cap the form accepts", () => {
		expect(QUICKRUN_DEFAULT_BUDGET).toBe("1");
		expect(quickRunProblems({ tasks: "5", trials: "1", budget: QUICKRUN_DEFAULT_BUDGET })).toEqual({});
	});
});
