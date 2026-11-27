import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { agentPathFor, canaryCostLine, newCanaryFields } from "./newCanaryForm";

const c = i18n.getFixedT("en", "uf7b") as never;

describe("drift monitor cost line (Q-34, Q-275)", () => {
	it("rounds per run, day and month the same way", () => {
		const line = canaryCostLine(c, { runs: 1, perRun: 0.005, day: 0.005, month: 0.15 }, "daily");
		expect(line).toBe("At most 1 run a day, up to $0.0050 a run, at most $0.0050 a day and $0.15 a month.");
	});
	it("says when the daily limit stops an hourly monitor", () => {
		const line = canaryCostLine(c, { runs: 3, perRun: 1, day: 3, month: 90 }, "hourly");
		expect(line.startsWith("Every hour, but the daily limit stops it after 3 runs.")).toBe(true);
		expect(canaryCostLine(c, { runs: 24, perRun: 0.1, day: 2.4, month: 72 }, "hourly")).not.toContain("Every hour");
	});
});

describe("Watch this agent for drift (Q-50)", () => {
	it("seeds the agent and maps an id to its path", () => {
		expect(newCanaryFields("codex").agent).toBe("codex");
		expect(newCanaryFields().agent).toBe("");
		const agents = [{ id: "codex", path: "agents/codex.yaml" }];
		expect(agentPathFor(agents, "codex")).toBe("agents/codex.yaml");
		expect(agentPathFor(agents, "agents/codex.yaml")).toBe("agents/codex.yaml");
		expect(agentPathFor(agents, "other")).toBe("other");
	});
});
