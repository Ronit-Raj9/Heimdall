import { describe, expect, it } from "vitest";
import { preferredAgentEvaluation } from "./agentEvaluation";

describe("preferredAgentEvaluation (Q-17)", () => {
	const list = [
		{ id: "ev_t", name: "zz-test-c2-budget" },
		{ id: "ev_f", name: "Failure test: codex" },
		{ id: "ev_p", name: "Codex weekly" },
	];
	it("skips a test object the server picked and opens the newest plain evaluation", () => {
		expect(preferredAgentEvaluation({ evaluation_id: "ev_t", evaluation_name: "zz-test-c2-budget", evaluations: list })).toBe("ev_p");
	});
	it("skips a failure test", () => {
		expect(preferredAgentEvaluation({ evaluation_id: "ev_f", evaluations: list })).toBe("ev_p");
	});
	it("keeps a plain pick", () => {
		expect(preferredAgentEvaluation({ evaluation_id: "ev_p", evaluations: list })).toBeUndefined();
	});
	it("leaves a test object for a failure test when nothing plain is left", () => {
		expect(preferredAgentEvaluation({ evaluation_id: "ev_t", evaluations: list.slice(0, 2) })).toBe("ev_f");
		expect(preferredAgentEvaluation({ evaluation_id: "ev_f", evaluations: list.slice(0, 2) })).toBeUndefined();
	});
});
