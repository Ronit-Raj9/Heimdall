import { describe, expect, it } from "vitest";
import { metricSources } from "./metricStrip";

describe("metricSources (M4-58)", () => {
	it("lists each evidence source once, in first-seen order", () => {
		const lanes = [
			{ arm: "A", metrics: { steps: { state: "observed", v: 1, evidence: "gateway" }, cost: { state: "observed", v: 2, evidence: "trace" } } },
			{ arm: "B", metrics: { steps: { state: "observed", v: 3, evidence: "gateway" }, cost: { state: "unknown" } } },
		] as never;
		expect(metricSources(lanes)).toEqual(["gateway", "trace"]);
		expect(metricSources([])).toEqual([]);
	});
});
