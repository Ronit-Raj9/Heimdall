import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { RedTeamCatalog } from "@/lib/store/apis/redteamApi";
import { planFrom, RegressionsList, taskCount } from "./redTeamPanel";

vi.mock("@/components/jobs/jobRunButton", () => ({ JobRunButton: () => null }));
vi.mock("@/components/models/modelPicker", () => ({ ModelPicker: () => null }));

const catalog: RedTeamCatalog = {
	categories: [
		{ id: "prompt_leak", label: "Instruction leak", description: "", channels: ["chat"] },
		{ id: "goal_hijack", label: "Goal hijack", description: "", channels: ["tool_result", "document", "email"] },
	],
	strategies: [
		{ id: "pair", label: "PAIR", description: "" },
		{ id: "tap", label: "Tree of attacks", description: "" },
	],
	channels: [
		{ id: "chat", label: "Chat", description: "" },
		{ id: "email", label: "Emails", description: "" },
	],
	suite: "red-team",
	max_attempts: 6,
	regressions: "regressions",
};

describe("red-team plan", () => {
	it("counts tasks per offered channel and strategy", () => {
		expect(taskCount(catalog, ["prompt_leak", "goal_hijack"], ["pair", "tap"], ["chat", "email"])).toBe(4);
		expect(taskCount(catalog, ["goal_hijack"], ["pair"], ["chat"])).toBe(0);
	});

	it("needs a model and a fitting choice", () => {
		const base = { categories: ["prompt_leak"], strategies: ["pair"], channels: ["chat"], attempts: 6 };
		expect(planFrom({ ...base, model: null }, catalog).problem).toMatch(/model/);
		expect(planFrom({ ...base, channels: ["email"], model: { provider: "openai", model: "m" } }, catalog).problem).toMatch(/is offered on the channels/);
		const ok = planFrom({ ...base, model: { provider: "openai", model: "gpt-4o-mini" } }, catalog);
		expect(ok.plan).toEqual({ suite: "red-team", categories: ["prompt_leak"], strategies: ["pair"], channels: ["chat"], max_attempts: 6, attacker_provider: "openai", attacker_model: "gpt-4o-mini" });
	});

	it("lists regressions with their review state", () => {
		const html = renderToStaticMarkup(
			<RegressionsList
				items={[
					{ task: "reg.prompt_leak.pair.chat.abc", state: "pending", category: "prompt_leak", strategy: "pair", channel: "chat", run: "r1", source_suite: "red-team", source_task: "rt.prompt_leak.pair.chat", payload_sha256: "x", created: "", attempts_to_break: 2 },
					{ task: "reg.goal_hijack.tap.email.def", state: "accepted", category: "goal_hijack", strategy: "tap", channel: "email", run: "r2", source_suite: "red-team", source_task: "t", payload_sha256: "y", created: "" },
				]}
				onDecide={() => {}}
			/>,
		);
		expect(html).toContain("1 waiting for review");
		expect(html).toContain("broke after 2 attempts");
		expect(html).toContain("red-team-accept-reg.prompt_leak.pair.chat.abc");
		expect(html).not.toContain("red-team-accept-reg.goal_hijack.tap.email.def");
	});
});
