import { runDivergencePath } from "@/lib/store/apis/runDivergenceApi";
import { describe, expect, it } from "vitest";

describe("compare with another run", () => {
	it("requests GET /runs/{id}/divergence/{other} with escaped segments", () => {
		expect(runDivergencePath("r/1", "r 2")).toBe("/runs/r%2F1/divergence/r%202");
	});
});