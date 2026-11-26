import { describe, expect, it } from "vitest";
import { AGENT_RUNS_PAGE, agentRunsParams } from "./agentRunsTab";

describe("agent runs params (wave 4, p01-3)", () => {
	it("asks the server for failed runs only when the box is ticked", () => {
		expect(agentRunsParams("bot", 0, true)).toEqual({ agent: "bot", limit: AGENT_RUNS_PAGE, offset: 0, failed: true });
		expect(agentRunsParams("bot", 2, false)).toEqual({ agent: "bot", limit: AGENT_RUNS_PAGE, offset: 2 * AGENT_RUNS_PAGE });
	});
});