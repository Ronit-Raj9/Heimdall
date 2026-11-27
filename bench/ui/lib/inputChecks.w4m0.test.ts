import { describe, expect, it } from "vitest";
import "@/lib/i18n";
import { idProblem, wholeAtLeastZero, wholeNumberProblem } from "@/lib/inputChecks";

describe("wholeNumberProblem (wave 4)", () => {
	it("explains every value outside 1 to 500", () => {
		for (const raw of ["0", "", "3.5", "501"]) expect(wholeNumberProblem(raw, 1, 500)).toBe("Enter a whole number from 1 to 500.");
	});
	it("is null for a whole number in range", () => {
		expect(wholeNumberProblem("3", 1, 500)).toBeNull();
		expect(wholeNumberProblem(500, 1, 500)).toBeNull();
	});
});

describe("idProblem (wave 4)", () => {
	it("asks for an empty id and explains a bad one", () => {
		expect(idProblem("  ", "benchmark id")).toBe("Enter the benchmark id.");
		expect(idProblem("Bad Id", "benchmark id")).toBe(
			"The benchmark id can use lowercase letters, digits, dots, dashes and underscores. Start with a letter or digit.",
		);
		expect(idProblem("-x", "benchmark id")).not.toBeNull();
	});
	it("is null for a valid id", () => {
		expect(idProblem("my-suite_1.2", "benchmark id")).toBeNull();
	});
});

describe("wholeAtLeastZero (wave 4)", () => {
	it("truncates and floors at zero", () => {
		expect(wholeAtLeastZero("3.7")).toBe(3);
		expect(wholeAtLeastZero(-5)).toBe(0);
		expect(wholeAtLeastZero("")).toBe(0);
		expect(wholeAtLeastZero("abc")).toBe(0);
		expect(wholeAtLeastZero(12)).toBe(12);
	});
});