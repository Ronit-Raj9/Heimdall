import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { diagnosticsFixture, verdictFixture, verdictWith } from "./testFixtures";
import { armOrder } from "./verdict.utils";
import { VerdictSummary } from "./verdictSummary";

const arms = armOrder(verdictFixture);
const render = (overrides: Parameters<typeof verdictWith>[0]) =>
	renderToStaticMarkup(
		<VerdictSummary verdict={verdictWith(overrides)} arms={arms} naive={verdictFixture.naive} armMetrics={diagnosticsFixture.arm_metrics} onNext={() => undefined} />,
	);

describe("VerdictSummary", () => {
	it("says which arm is better and by how much", () => {
		const html = render({ decision: "improved", action: "ship" });
		expect(html).toMatch(new RegExp(`${arms[1]} is better by [0-9.]+ pp`));
		expect(html).toContain(">Export report<");
	});
	it("says no clear difference and invalid plainly", () => {
		expect(render({ decision: "inconclusive", action: "no_decision" })).toContain("No clear difference yet");
		expect(render({ decision: "invalid", action: "fix_harness" })).toContain("Invalid: fix the harness");
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
