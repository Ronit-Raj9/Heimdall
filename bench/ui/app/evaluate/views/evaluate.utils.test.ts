import { pickableAgents } from "@/components/agents/agentPicker";
import { describe, expect, it } from "vitest";
import { latestVersion, splitAgents, type AgentWithCaps } from "./evaluate.utils";

const agent = (id: string, extra: Partial<AgentWithCaps> = {}) =>
	({ id, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra }) as AgentWithCaps;

describe("splitAgents (N97)", () => {
	it("lists the same agents as every other agent picker", () => {
		const agents = [
			agent("codex"),
			agent("opencode"),
			agent("my-bot", { adapter: "remote_http" }),
			agent("oracle", { scripted_kind: "oracle" }),
			agent("storm"),
			agent("acme-agent", { adapter: "scripted" }),
			agent("shop-demo", { description: "A demo agent for the example benchmarks" } as Partial<AgentWithCaps>),
			agent("live-l10-codex-task"),
			agent("broken-file", { error: "bad yaml" }),
		];
		const { own, builtin } = splitAgents(agents);
		const { yours, baselines } = pickableAgents(agents);
		const ids = (xs: { id: string }[]) => xs.map((a) => a.id).sort();
		expect(ids([...own, ...builtin])).toEqual(ids([...yours, ...baselines]));
		expect(own.map((a) => a.id)).toEqual(["my-bot"]);
		expect(builtin.map((a) => a.id)).toEqual(["codex", "opencode"]);
	});

	it("puts an agent the server marks built-in under built-in", () => {
		const { own, builtin } = splitAgents([agent("bare-model", { builtin: true }), agent("mine", { builtin: false })]);
		expect(own.map((a) => a.id)).toEqual(["mine"]);
		expect(builtin.map((a) => a.id)).toEqual(["bare-model"]);
	});
});

describe("latestVersion (N95)", () => {
	it("is the highest saved version", () => {
		expect(latestVersion([{ version: 1 }, { version: 3 }, { version: 2 }])).toBe(3);
		expect(latestVersion([])).toBeUndefined();
		expect(latestVersion(undefined)).toBeUndefined();
	});
});