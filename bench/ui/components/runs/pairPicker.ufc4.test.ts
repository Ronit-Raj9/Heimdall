import { describe, expect, it } from "vitest";
import { setupNames } from "./pairPicker";

describe("pair picker names both setups (X-33)", () => {
	it("names each arm with its agent and model, in the grid's order", () => {
		const setups = [
			{ arm: "B", agent: "codex", model: "gpt-5" },
			{ arm: "A", agent: "claude-code", model: "" },
		];
		expect(setupNames(["A", "B"], setups)).toEqual(["A: claude-code", "B: codex gpt-5"]);
	});

	it("names nothing when the answer has no setups", () => {
		expect(setupNames(["A", "B"], undefined)).toEqual([]);
		expect(setupNames(["A"], [{ arm: "B", agent: "x", model: "y" }])).toEqual([]);
	});
});
