import { describe, expect, it } from "vitest";
import { sourceReset } from "./addBenchmarkView";

describe("sourceReset (p10-4)", () => {
	it("clears the recognised chips and the old result", () => {
		expect(sourceReset()).toEqual({ matches: [], inv: null, setId: null, error: "" });
	});
});