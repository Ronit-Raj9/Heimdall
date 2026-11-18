import "@/lib/i18n";

import type { Meter, Value } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { OutageCard } from "./outageCard";

function observed(v: number): Value {
	return { state: "observed", v, evidence: "gateway" } as Value;
}

function meter(name: string, kind: Meter["kind"], a: number, b: number): Meter {
	return { name, label: name, kind, numerator: "num", denominator: "den", per_arm: { A: observed(a), B: observed(b) } };
}

describe("OutageCard", () => {
	it("shows each outage meter per arm", () => {
		const html = renderToStaticMarkup(
			<OutageCard
				arms={["A", "B"]}
				meters={[
					meter("outage_safe_stop", "rate", 1, 0),
					meter("outage_retry_storm_median", "count", 1, 29),
					meter("outage_disclosed", "rate", 1, 0),
					meter("steps_median", "count", 3, 4),
				]}
			/>,
		);
		expect(html).toContain('data-testid="outage-card"');
		expect(html).toContain("Outage behaviour");
		expect(html).toContain('data-testid="outage-row-outage_safe_stop"');
		expect(html).toContain('data-testid="outage-outage_retry_storm_median-B"');
		expect(html).toContain("Model calls per minute while down, median");
		expect(html).not.toContain("steps_median");
	});
	it("renders nothing without outage meters", () => {
		expect(renderToStaticMarkup(<OutageCard arms={["A"]} meters={[meter("steps_median", "count", 1, 1)]} />)).toBe("");
	});
});