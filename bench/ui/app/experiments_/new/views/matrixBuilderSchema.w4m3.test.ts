import { describe, expect, it } from "vitest";
import { clearDefaultModels } from "./matrixBuilderSchema";

const defaults = [
	{ provider: "openai", model: "gpt-5.4-mini" },
	{ provider: "openai", model: "" },
] as const;

describe("clearDefaultModels (p06-6)", () => {
	it("clears the prefilled model when nobody changed it", () => {
		expect(clearDefaultModels([...defaults], [...defaults])).toEqual([
			{ provider: "openai", model: "" },
			{ provider: "openai", model: "" },
		]);
	});
	it("keeps models the person changed", () => {
		const mine = [{ provider: "anthropic", model: "claude-sonnet" }];
		expect(clearDefaultModels(mine, [...defaults])).toBe(mine);
	});
});