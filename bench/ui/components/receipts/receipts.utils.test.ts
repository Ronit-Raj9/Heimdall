import { RECEIPT_STEPS, type Catalog, type ProbeSpec } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { LAYER_STEPS, catalogProbeFor, ratioShares, stepApplies } from "./receipts.utils";

describe("stepApplies", () => {
	it("follows each layer's declared steps (DESIGN.md 10.3)", () => {
		expect(LAYER_STEPS.gateway).toEqual(RECEIPT_STEPS);
		expect(stepApplies("fired", "fixture", undefined)).toBe(false);
		expect(stepApplies("visible", "fixture", undefined)).toBe(true);
		expect(stepApplies("fired", "tool_shim", undefined)).toBe(true);
		expect(stepApplies("delivered", "tool_shim", undefined)).toBe(false);
		expect(stepApplies("absorbed_by", "gateway", undefined)).toBe(true);
	});

	it("infers from the data when the layer is unknown", () => {
		const ratios = { A: { fired: { num: 1, den: 2, unknown: 0 } }, B: {} };
		expect(stepApplies("fired", null, ratios)).toBe(true);
		expect(stepApplies("delivered", null, ratios)).toBe(false);
	});
});

describe("ratioShares", () => {
	it("keeps unknowns as their own share", () => {
		expect(ratioShares({ num: 2, den: 12, unknown: 1 })).toEqual({ yes: 2 / 12, unknown: 1 / 12 });
	});
	it("has no shares without a denominator", () => {
		expect(ratioShares({ num: 0, den: 0, unknown: 0 })).toBeNull();
		expect(ratioShares(undefined)).toBeNull();
	});
	it("never overflows the bar", () => {
		expect(ratioShares({ num: 5, den: 4, unknown: 3 })).toEqual({ yes: 1, unknown: 0 });
	});
});
// 6-C-05: the four newer probe layers have readable names, and an unknown one never shows its key.
describe("probeLayerLabel", () => {
	it("names every layer of the Go catalog and falls back to the literal", async () => {
		const i18n = (await import("@/lib/i18n")).default;
		const t = i18n.getFixedT("en", "bench");
		const { probeLayerLabel } = await import("./receipts.utils");
		expect(probeLayerLabel("a2a", t)).toBe("A2A stream");
		for (const layer of ["adapter", "tool", "service", "tool_shim"]) expect(probeLayerLabel(layer, t)).not.toContain("receipts.");
		expect(probeLayerLabel("future_layer", t)).toBe("future layer");
	});
});

describe("catalogProbeFor", () => {
	const probe = (layer: ProbeSpec["layer"]) => ({ id: "shared-probe", layer }) as ProbeSpec;
	const catalog = {
		suites: [
			{ id: "first", version: "1.0.0", tasks: [], probes: ["shared-probe"], probe_layers: { "shared-probe": "fixture" } },
			{ id: "second", version: "2.0.0", tasks: [], probes: ["shared-probe"], probe_layers: { "shared-probe": "gateway" } },
		],
		probes: [probe("fixture"), probe("gateway")],
	} as unknown as Catalog;

	it("uses the experiment's own suite when two suites share a probe id", () => {
		expect(catalogProbeFor(catalog, "second@2.0.0", "shared-probe")?.layer).toBe("gateway");
		expect(catalogProbeFor(catalog, "first", "shared-probe")?.layer).toBe("fixture");
		const fired = { A: { fired: { num: 1, den: 1, unknown: 0 } } };
		expect(stepApplies("fired", catalogProbeFor(catalog, "second@2.0.0", "shared-probe")?.layer, fired)).toBe(true);
	});

	it("does not guess between suites when the suite is unknown", () => {
		expect(catalogProbeFor(catalog, undefined, "shared-probe")).toBeUndefined();
		expect(catalogProbeFor(catalog, "other", "missing")).toBeUndefined();
	});
});
