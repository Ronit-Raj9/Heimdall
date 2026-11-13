import type { ValueKind } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { describeValue, formatValueNumber } from "./valueCell.utils";

describe("formatValueNumber", () => {
	const cases: { name: string; v: number; kind: ValueKind; want: string }[] = [
		{ name: "rate whole", v: 0.5, kind: "rate", want: "50%" },
		{ name: "rate fraction", v: 0.4167, kind: "rate", want: "41.7%" },
		{ name: "rate negative", v: -0.0912, kind: "rate", want: "-9.1%" },
		{ name: "count integer", v: 1234, kind: "count", want: "1,234" },
		{ name: "count median", v: 20.5, kind: "count", want: "20.5" },
		{ name: "tokens small", v: 812, kind: "tokens", want: "812" },
		{ name: "tokens compact", v: 182400, kind: "tokens", want: "182.4K" },
		{ name: "usd zero", v: 0, kind: "usd", want: "$0.00" },
		{ name: "usd sub-cent", v: 0.0031, kind: "usd", want: "$0.0031" },
		{ name: "usd cents", v: 0.41, kind: "usd", want: "$0.41" },
		{ name: "usd dollars", v: 16.84, kind: "usd", want: "$16.84" },
		{ name: "duration seconds", v: 14.25, kind: "duration", want: "14.3s" },
		{ name: "duration minutes", v: 312, kind: "duration", want: "5.2m" },
		{ name: "duration hours", v: 3600, kind: "duration", want: "1h" },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(formatValueNumber(c.v, c.kind, "en")).toBe(c.want);
		});
	}
});

describe("describeValue", () => {
	it("observed", () => {
		expect(describeValue({ state: "observed", v: 0.75, evidence: "outcome" }, "rate", "en")).toEqual({
			state: "observed",
			text: "75%",
			evidence: "outcome",
		});
	});

	it("unknown with bounds", () => {
		expect(describeValue({ state: "unknown", v: null, bounds: [0.33, 0.37], reason: "telemetry_incomplete" }, "usd", "en")).toEqual({
			state: "unknown",
			reason: "telemetry_incomplete",
			bounds: ["$0.33", "$0.37"],
		});
	});

	it("unknown without reason or bounds", () => {
		expect(describeValue({ state: "unknown", v: null }, "count", "en")).toEqual({ state: "unknown", reason: null, bounds: null });
	});

	it("not applicable", () => {
		expect(describeValue({ state: "not_applicable", v: null }, "duration", "en")).toEqual({ state: "not_applicable" });
	});

	it("missing value is unknown", () => {
		expect(describeValue(null, "rate", "en")).toEqual({ state: "unknown", reason: null, bounds: null });
	});

	it("observed NaN is unknown", () => {
		expect(describeValue({ state: "observed", v: Number.NaN }, "rate", "en").state).toBe("unknown");
	});
});