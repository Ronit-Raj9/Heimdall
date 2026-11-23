import { describe, expect, it } from "vitest";
import { formatUsd } from "./money";

describe("formatUsd", () => {
	it("prints every amount with enough digits (fix wave 3, N190 and N207)", () => {
		const table: [number | null | undefined, string][] = [
			[null, "-"],
			[undefined, "-"],
			[Number.NaN, "-"],
			[0, "$0"],
			[0.001, "$0.0010"],
			[0.0094, "$0.0094"],
			[0.01, "$0.01"],
			[1, "$1.00"],
			[9.994, "$9.99"],
			[12.4, "$12"],
			[1234.5, "$1,235"],
			[-0.5, "-$0.50"],
		];
		for (const [v, want] of table) expect(formatUsd(v)).toBe(want);
	});
});