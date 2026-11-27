import { describe, expect, it } from "vitest";
import { builderLink, clampInt } from "./inventoryPanel";
import { badSize } from "@/components/workers/connectedWorkers";

describe("add a benchmark and connect a worker helpers (fix wave lane 2a)", () => {
	it("sends OpenAPI and GitHub sources to the builder that makes their tasks (M2-112, M6-50)", () => {
		expect(builderLink("openapi")).toBe("/suites/from-api");
		expect(builderLink("github")).toBe("/suites/new");
		expect(builderLink("rows")).toBeNull();
	});

	it("clamps the limit and the held-out share (M2-112b)", () => {
		expect(clampInt(900, 1, 500)).toBe(500);
		expect(clampInt(0, 1, 500)).toBe(1);
		expect(clampInt(-5, 0, 100)).toBe(0);
		expect(clampInt(Number.NaN, 0, 100)).toBe(0);
	});

	it("names a machine size below 1 (M8-34)", () => {
		expect(badSize("0")).toBe(true);
		expect(badSize("-2")).toBe(true);
		expect(badSize("abc")).toBe(true);
		expect(badSize("")).toBe(false);
		expect(badSize(undefined)).toBe(false);
		expect(badSize("2")).toBe(false);
	});
});
