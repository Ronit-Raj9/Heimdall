import "@/lib/i18n";

import type { PolicyDecision, PolicyResult } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PolicyScorecard } from "./policyScorecard";
import { verdictFixture } from "./testFixtures";

const fixturePolicy = verdictFixture.policy as PolicyResult;

function policyWith(decision: PolicyDecision, checks: PolicyResult["checks"]): PolicyResult {
	return { ...structuredClone(fixturePolicy), decision, checks };
}

const passCheck = fixturePolicy.checks.find((c) => c.id === "no-integrity-violations")!;
const warnUnknown = fixturePolicy.checks.find((c) => c.id === "cost-per-verified-success")!;

describe("PolicyScorecard", () => {
	const cases: { decision: PolicyDecision; policy: PolicyResult; text: string; tone: string; className: string }[] = [
		{ decision: "allow", policy: policyWith("allow", [passCheck]), text: ">ALLOW<", tone: "good", className: "text-chart-success-ink" },
		{
			decision: "warn",
			policy: policyWith("warn", [passCheck, warnUnknown]),
			text: ">WARN<",
			tone: "warn",
			className: "text-chart-warning-ink",
		},
		{ decision: "block", policy: fixturePolicy, text: ">BLOCK<", tone: "bad", className: "text-chart-error-ink" },
	];
	for (const c of cases) {
		it(`renders a ${c.decision} decision`, () => {
			const html = renderToStaticMarkup(<PolicyScorecard policy={c.policy} expId="e_x" />);
			expect(html).toContain(`data-decision="${c.decision}"`);
			expect(html).toMatch(new RegExp(`data-testid="policy-decision" data-tone="${c.tone}"`));
			expect(html).toContain(c.text);
			expect(html).toContain(c.className);
			for (const check of c.policy.checks) expect(html).toContain(`data-testid="policy-check-row-${check.id}"`);
		});
	}

	it("shows unknown as unknown, never as pass", () => {
		const html = renderToStaticMarkup(<PolicyScorecard policy={policyWith("warn", [warnUnknown])} expId="e_x" />);
		expect(html).toMatch(/data-testid="policy-check-row-cost-per-verified-success" data-status="unknown" data-blocking="false"/);
		expect(html).toContain('data-testid="policy-check-observed-cost-per-verified-success"');
		expect(html).toContain('data-state="unknown"');
		expect(html).toContain("bench-hatched");
		expect(html).not.toContain(">pass<");
		expect(html).toContain(">unknown<");
	});

	it("marks a blocking failure and lists its evidence as links when the pair is known", () => {
		const html = renderToStaticMarkup(<PolicyScorecard policy={fixturePolicy} expId="e_x" runPairs={{ r_1w30xridyv: "p_ygsluimr7n" }} />);
		expect(html).toMatch(/data-testid="policy-check-row-false-completion-ceiling" data-status="fail" data-blocking="true"/);
		expect(html).toContain('data-testid="policy-check-blocking-false-completion-ceiling"');
		expect(html).toContain('href="/runs?pair=p_ygsluimr7n&amp;exp=e_x"');
		expect(html).toContain('data-testid="policy-evidence-link-r_1w30xridyv"');
		// No pair known: the id is shown but not linked.
		expect(html).toContain(">r_efejyyax55</code>");
		expect(html).toContain("&lt;= 0.05");
	});

	it("shows a rule's observed value as signed percentage points", () => {
		const html = renderToStaticMarkup(<PolicyScorecard policy={fixturePolicy} expId="e_x" />);
		expect(html).toMatch(/data-testid="policy-check-observed-clean-no-regression">\+8\.3 pp</);
	});

	it("counts the results and shows the hash", () => {
		const html = renderToStaticMarkup(<PolicyScorecard policy={fixturePolicy} expId="e_x" />);
		expect(html).toContain("3 pass, 1 fail, 1 unknown");
		expect(html).toContain("enterprise-default, sha256 5c1e0f9a…e6f7");
	});

	it("says so when no policy applies", () => {
		expect(renderToStaticMarkup(<PolicyScorecard policy={undefined} expId="e_x" />)).toContain('data-testid="policy-scorecard-none"');
	});
});