import "@/lib/i18n";

import type { Action, ExperimentStatus, ReleasePolicyDecision } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReleaseForm } from "./releaseForm";
import type { ReleaseFormValues } from "./releases.utils";

const experiment: ExperimentStatus = {
	exp_id: "e_synth_notes",
	name: "notes v2",
	state: "done",
	manifest_hash: "sha256:m",
	frozen_at: "2026-11-10T09:00:00Z",
	slots: { total: 4, done: 4, running: 0, failed: 0 },
	spent_usd: { state: "unknown", v: null, reason: "synthetic" },
};

function render(action: Action, policy: ReleasePolicyDecision | undefined, values: Partial<ReleaseFormValues>): string {
	return renderToStaticMarkup(
		<ReleaseForm
			experiments={[experiment]}
			expId="e_synth_notes"
			onExpChange={() => {}}
			verdict={{ analysisId: "a_synth001", decision: action === "ship" ? "improved" : "inconclusive", action }}
			policy={policy}
			initialValues={values}
			onSubmit={() => {}}
		/>,
	);
}

/** The release button's opening tag. */
function submitButton(html: string): string {
	const match = html.match(/<button[^>]*data-testid="release-submit"[^>]*>/);
	expect(match).not.toBeNull();
	return match![0];
}

describe("ReleaseForm", () => {
	const cases: {
		name: string;
		action: Action;
		policy?: ReleasePolicyDecision;
		values: Partial<ReleaseFormValues>;
		enabled: boolean;
		blocked: boolean;
	}[] = [
		{
			name: "ship and allow with an approver",
			action: "ship",
			policy: "allow",
			values: { approver: "maya" },
			enabled: true,
			blocked: false,
		},
		{ name: "ship with no policy", action: "ship", values: { approver: "maya" }, enabled: true, blocked: false },
		{ name: "ship without an approver", action: "ship", policy: "allow", values: {}, enabled: false, blocked: false },
		{ name: "hold", action: "hold", values: { approver: "maya" }, enabled: false, blocked: true },
		{ name: "ship but policy blocks", action: "ship", policy: "block", values: { approver: "maya" }, enabled: false, blocked: true },
		{
			name: "override ticked without a reason",
			action: "hold",
			values: { approver: "maya", override: true },
			enabled: false,
			blocked: true,
		},
		{
			name: "override with a reason",
			action: "ship",
			policy: "block",
			values: { approver: "maya", override: true, overrideReason: "hotfix" },
			enabled: true,
			blocked: true,
		},
	];
	for (const c of cases) {
		it(c.name, () => {
			const html = render(c.action, c.policy, c.values);
			expect(/ disabled=""/.test(submitButton(html))).toBe(!c.enabled);
			expect(html.includes('data-testid="release-override-checkbox"')).toBe(c.blocked);
			expect(html.includes('data-testid="release-blocked"')).toBe(c.blocked);
		});
	}

	it("shows the verdict action and policy decision it would cite", () => {
		const html = render("ship", "warn", { approver: "maya" });
		expect(html).toContain("a_synth001");
		expect(html).toMatch(/data-testid="release-verdict-action"[^>]*>ship</);
		expect(html).toMatch(/data-testid="release-verdict-policy"[^>]*>warn</);
	});

	it("names each blocker", () => {
		const html = render("no_decision", "block", { approver: "maya" });
		expect(html).toContain("The verdict action is no_decision, not ship.");
		expect(html).toContain("The policy decision is block.");
		expect(html).not.toContain('data-testid="release-override-reason"');
	});

	it("is disabled until a verdict loads", () => {
		const html = renderToStaticMarkup(
			<ReleaseForm
				experiments={[experiment]}
				expId="e_synth_notes"
				onExpChange={() => {}}
				verdictLoading
				initialValues={{ approver: "maya" }}
				onSubmit={() => {}}
			/>,
		);
		expect(submitButton(html)).toMatch(/ disabled=""/);
		expect(html).toContain('data-testid="release-verdict-loading"');
	});
});