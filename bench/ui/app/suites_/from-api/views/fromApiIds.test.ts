import { describe, expect, it } from "vitest";
import { appIdFrom, idProblems, maxUsdProblem, specTitle, suiteIdFrom } from "./fromApiIds";

describe("From API ids (M6-57)", () => {
	it("reads info.title from JSON and YAML", () => {
		expect(specTitle('{"openapi":"3.0.0","info":{"title":"Shop API"}}')).toBe("Shop API");
		expect(specTitle("openapi: 3.0.0\ninfo:\n  title: 'Shop API'\n  version: 1\npaths: {}\n")).toBe("Shop API");
		expect(specTitle("")).toBe("");
	});

	it("drafts the two ids from the title", () => {
		expect(appIdFrom("Shop API")).toBe("shop");
		expect(appIdFrom("Pet Store Service v2")).toBe("pet_store");
		expect(appIdFrom("Customer")).toBe("");
		expect(suiteIdFrom("pet_store")).toBe("pet-store-support");
	});

	it("names the wrong field", () => {
		expect(idProblems("shop-support", "shop")).toEqual([]);
		expect(idProblems("Bad", "shop")).toEqual(["benchmark"]);
		expect(idProblems("ok", "customer")).toEqual(["appReserved"]);
		expect(idProblems("ok", "9x")).toEqual(["app"]);
	});

	it("refuses a cost cap that is not a positive number", () => {
		expect(maxUsdProblem("")).toBe(false);
		expect(maxUsdProblem("2.5")).toBe(false);
		expect(maxUsdProblem("abc")).toBe(true);
		expect(maxUsdProblem("0")).toBe(true);
	});
});
