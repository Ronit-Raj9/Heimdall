import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { diagnosticsFixture, verdictFixture, verdictWith } from "./testFixtures";
import { armOrder } from "./verdict.utils";
import { VerdictSummary } from "./verdictSummary";

const arms = armOrder(verdictFixture);
const render = (overrides: Parameters<typeof verdictWith>[0]) =>
	renderToStaticMarkup(
		<VerdictSummary
			verdict={verdictWith(overrides)}
			arms={arms}
			naive={verdictFixture.naive}
			armMetrics={diagnosticsFixture.arm_metrics}
			onNext={() => undefined}
		/>,
	);

describe("VerdictSummary", () => {
	it("says which arm is better and by how much", () => {
		const html = render({ decision: "improved", action: "ship" });
		expect(html).toMatch(new RegExp(`B \\(${arms[1]}\\) is better by [0-9.]+ pp`));
		expect(html).toContain(">Export report<");
	});
	it("says no clear difference and invalid plainly", () => {
		expect(render({ decision: "inconclusive", action: "no_decision" })).toContain("No clear winner yet");
		expect(render({ decision: "invalid", action: "fix_harness" })).toContain("Test was broken");
	});
	it("shows a pass rate bar, cost and tokens per arm", () => {
		const html = render({ decision: "inconclusive", action: "no_decision" });
		for (const arm of arms) {
			expect(html).toContain(`data-testid="verdict-summary-arm-${arm}"`);
			expect(html).toContain(`data-testid="verdict-summary-cost-${arm}"`);
		}
		expect(html).toContain('role="meter"');
	});
});

describe("VerdictSummary cost and tokens (D8.cost)", () => {
	// The live payload of e_eval_20261122_143334_423797_hd_harbor_bf_le4r: arms A and B, observed gateway costs.
	const live = {
		A: {
			cost_usd: { state: "observed" as const, v: 0.0035288, evidence: "gateway" as const },
			tokens: { state: "observed" as const, v: 23128, evidence: "gateway" as const },
		},
		B: {
			cost_usd: { state: "observed" as const, v: 0.0017, evidence: "gateway" as const },
			tokens: { state: "observed" as const, v: 11021, evidence: "gateway" as const },
		},
	};
	const at = (html: string, id: string) => html.match(new RegExp(`data-testid="${id}"[^>]*>([^<]*)<`))?.[1];
	const show = (props: Partial<Parameters<typeof VerdictSummary>[0]>) =>
		renderToStaticMarkup(
			<VerdictSummary verdict={verdictWith({ decision: "inconclusive", action: "no_decision" })} arms={["A", "B"]} naive={{}} {...props} />,
		);

	it("renders the live payload shape", () => {
		const html = show({ armMetrics: live });
		expect(at(html, "verdict-summary-cost-A")).toBe("$0.0035");
		expect(at(html, "verdict-summary-tokens-A")).toBe("23,128");
		expect(at(html, "verdict-summary-cost-B")).toBe("$0.0017");
	});

	it("says loading and load failure instead of a missing price", () => {
		expect(at(show({ metricsStatus: "loading" }), "verdict-summary-cost-A")).toBe("Loading...");
		const failed = show({ metricsStatus: "error" });
		expect(at(failed, "verdict-summary-cost-A")).toBe("Could not load the costs.");
		expect(failed).not.toContain("no price set");
	});

	it("gives the value's own reason and the price sentence only for an unpriced cost", () => {
		const unknown = (reason: string) => ({ A: { cost_usd: { state: "unknown" as const, v: null, reason, evidence: "gateway" as const } } });
		const lost = show({ armMetrics: unknown("gateway log incomplete") });
		expect(at(lost, "verdict-summary-cost-A")).toBe("Not known: gateway log incomplete");
		expect(lost).not.toContain("no price set");
		expect(at(show({ armMetrics: unknown("2 of 5 cost rows unpriced") }), "verdict-summary-cost-A")).toBe(
			"Not known: this model has no price set",
		);
	});
});