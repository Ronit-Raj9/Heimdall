import { describe, expect, it } from "vitest";
import { parseCap, proofHolds, startBody } from "./fromIssues";

describe("start cap (uft5)", () => {
	it("sends the cap per issue with the start body", () => {
		expect(startBody(true, "openai", "gpt-x", 1)).toEqual({ consent: true, provider: "openai", model: "gpt-x", budget_usd: 1 });
		expect(startBody(true, "", "", 2.5)).toEqual({ consent: true, budget_usd: 2.5 });
		expect(startBody(true, "openai", "gpt-x")).toEqual({ consent: true, provider: "openai", model: "gpt-x" });
	});
	it("takes a cap from 0.01 to 50 only", () => {
		expect(parseCap("1.00")).toBe(1);
		expect(parseCap(" 50 ")).toBe(50);
		for (const bad of ["", "0", "-1", "51", "abc"]) expect(parseCap(bad)).toBeNull();
	});
});

describe("proof line (uft5)", () => {
	it("holds only with tests the fix turned from failing to passing", () => {
		expect(proofHolds({ fail_to_pass: ["t::test_new"], pass_to_pass: [] })).toBe(true);
		expect(proofHolds({ fail_to_pass: null, pass_to_pass: ["t::a"] })).toBe(false);
	});
});