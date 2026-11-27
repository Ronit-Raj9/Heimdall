import { describe, expect, it } from "vitest";
import type { AgentWithCaps } from "./evaluate.utils";
import { freeCheckPlan } from "./freeCheck";

const agent = (id: string, extra: Partial<AgentWithCaps> = {}) =>
	({ id, path: `agents/${id}.yaml`, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra }) as AgentWithCaps;
const arm = (a: string) => ({ agent: a, provider: "", model: "" }) as never;

describe("freeCheckPlan (decision 2)", () => {
	const agents = [agent("mine"), agent("claude-base", { builtin: true }), agent("endpoint", { owns_model: true })];
	it("queues agent.check params for the person's own agent and first benchmark", () => {
		expect(freeCheckPlan({ arms: [arm("mine")], benchmarks: ["swe-mini", "other"] }, agents)).toEqual({
			ok: true,
			params: { agent: "agents/mine.yaml", suite: "swe-mini" },
			suite: "swe-mini",
		});
	});
	it("says why it cannot run", () => {
		expect(freeCheckPlan({ arms: [], benchmarks: ["x"] }, agents)).toEqual({ ok: false, reason: "noAgent" });
		expect(freeCheckPlan({ arms: [arm("claude-base")], benchmarks: ["x"] }, agents)).toMatchObject({ reason: "builtin" });
		expect(freeCheckPlan({ arms: [arm("endpoint")], benchmarks: ["x"] }, agents)).toMatchObject({ reason: "ownsModel" });
		expect(freeCheckPlan({ arms: [arm("mine")], benchmarks: [] }, agents)).toMatchObject({ reason: "noBenchmark" });
	});
});
