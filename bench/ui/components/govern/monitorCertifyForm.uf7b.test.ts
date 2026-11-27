import { describe, expect, it } from "vitest";
import { fprProblem } from "./monitorCertifyForm";

describe("false alarm rates check (M7-69)", () => {
	it("takes a comma list of numbers between 0 and 1", () => {
		expect(fprProblem("0.01,0.001")).toBe(false);
		expect(fprProblem("0.05")).toBe(false);
		expect(fprProblem("1")).toBe(true);
		expect(fprProblem("0")).toBe(true);
		expect(fprProblem("abc")).toBe(true);
		expect(fprProblem("0.01,")).toBe(true);
	});
});
