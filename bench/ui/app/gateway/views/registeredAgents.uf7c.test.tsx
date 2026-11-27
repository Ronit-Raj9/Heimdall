import { describe, expect, it } from "vitest";
import { agentIdFor, benchmarkRegisteredAgent } from "./registeredAgents";

describe("Registered agents (lane 7c)", () => {
	it("says the id the new agent gets, in the server's id rule", () => {
		expect(agentIdFor("Support Bot v2")).toBe("support-bot-v2");
		expect(agentIdFor("--x")).toBe("x");
	});

	it("sends that id with the import", async () => {
		const seen: unknown[] = [];
		const id = await benchmarkRegisteredAgent(
			{
				importAgent: async (a) => (seen.push(a), { agent_id: "support-bot", yaml: "id: support-bot" }),
				createAgent: async () => ({ id: "support-bot" }),
			},
			{ name: "Support Bot", model: "gpt-6", provider: "openai", id: agentIdFor("Support Bot") },
		);
		expect(id).toBe("support-bot");
		expect(seen).toEqual([{ name: "Support Bot", model: "gpt-6", provider: "openai", id: "support-bot" }]);
	});
});
