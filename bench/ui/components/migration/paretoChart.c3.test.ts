import { describe, expect, it } from "vitest";
import { paretoPointNames } from "./paretoChart";

describe("paretoPointNames (M4-4)", () => {
	it("names each setup with its model", () => {
		const n = paretoPointNames([
			{ id: "A", agent: "codex", model: "gpt-5.6-terra" },
			{ id: "B", agent: "codex", model: "gpt-6-luna" },
		]);
		expect([...n.values()]).toEqual(["A (gpt-5.6-terra)", "B (gpt-6-luna)"]);
	});
	it("uses the agent when every setup runs one model", () => {
		const n = paretoPointNames([
			{ id: "A", agent: "codex", model: "m" },
			{ id: "B", agent: "opencode", model: "m" },
		]);
		expect(n.get("B")).toBe("B (opencode)");
	});
});
