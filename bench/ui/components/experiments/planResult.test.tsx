import "@/lib/i18n";

import type { ExperimentPlan } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PlanResult, planBlocksStart } from "./planResult";

const BASE: ExperimentPlan = {
	slots: 48,
	pairs: 24,
	conditions: [
		{ condition: "clean", tasks: 4, trials: 3, slots: 24 },
		{ condition: "stale-notes", tasks: 4, trials: 3, slots: 24 },
	],
	mde: 62.41,
	mde_assumed_sd: true,
	sd: 0.3,
	est_wall_s: { state: "observed", v: 3600, evidence: "outcome" },
	est_usd: { state: "observed", v: 17.5, evidence: "gateway" },
	errors: [],
	warnings: [],
};

describe("PlanResult", () => {
	it("shows a blocking error and blocks the start", () => {
		const plan: ExperimentPlan = {
			...BASE,
			errors: ["arms A and B have the same config hash: an experiment whose arms do not differ cannot start"],
			warnings: ["With 4 tasks the sign-flip test cannot reach p < 0.05."],
		};
		const html = renderToStaticMarkup(<PlanResult plan={plan} />);
		expect(planBlocksStart(plan)).toBe(true);
		expect(html).toContain('data-blocked="true"');
		expect(html).toContain('data-testid="plan-errors"');
		expect(html).toContain('role="alert"');
		expect(html).toContain(">Blocks the start<");
		expect(html).toContain("arms A and B have the same config hash");
		expect(html).toContain('data-testid="plan-warnings"');
		expect(html).toContain("With 4 tasks the sign-flip test cannot reach p &lt; 0.05.");
		expect(html).not.toContain('data-testid="plan-no-issues"');
	});

	it("shows sizes, estimates and the MDE with the assumed-SD note", () => {
		const html = renderToStaticMarkup(<PlanResult plan={BASE} />);
		expect(planBlocksStart(BASE)).toBe(false);
		expect(html).toContain('data-blocked="false"');
		expect(html).toContain('data-testid="plan-no-issues"');
		expect(html).toContain(">48<");
		expect(html).toContain(">24<");
		expect(html).toContain(">62.4 pp<");
		expect(html).toContain('data-testid="plan-mde-assumed-sd"');
		expect(html).toContain("Uses an assumed SD of 0.3");
		expect(html).toContain(">1h<");
		expect(html).toContain(">$17.50<");
		expect(html).toContain('data-testid="plan-condition-stale-notes"');
	});

	it("labels a measured SD as measured", () => {
		const html = renderToStaticMarkup(<PlanResult plan={{ ...BASE, mde_assumed_sd: false, sd: 0.21 }} />);
		expect(html).toContain('data-testid="plan-mde-sd"');
		expect(html).toContain("SD 0.21, measured");
	});

	it("renders an unknown estimate as unknown, never zero", () => {
		const html = renderToStaticMarkup(
			<PlanResult plan={{ ...BASE, est_usd: { state: "unknown", v: null, reason: "no_price_snapshot" } }} />,
		);
		expect(html).toContain('data-testid="plan-est-cost-value"');
		expect(html).toContain("Unknown: no_price_snapshot.");
	});
});