import { describe, expect, it } from "vitest";
import { pageWidthClass } from "./pageWidth";

describe("pageWidthClass (Q-317)", () => {
	it("maps each kind to its capped width", () => {
		expect(pageWidthClass("wide")).toContain("max-w-(--page-max-wide)");
		expect(pageWidthClass("dash")).toContain("max-w-(--page-max-dash)");
		expect(pageWidthClass("read")).toContain("max-w-(--page-max-read)");
		for (const k of ["wide", "dash", "read"] as const) {
			expect(pageWidthClass(k)).toContain("w-full");
			expect(pageWidthClass(k)).toContain("min-w-0");
			expect(pageWidthClass(k)).toContain("mx-auto");
		}
	});
});
