import "@/lib/i18n";

import type { Effect, Verdict } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { verdictFixture, verdictWith } from "./testFixtures";
import { VerdictCard } from "./verdictCard";

const baseEffect = verdictFixture.verdict.effect as Effect;

function effectWith(overrides: Partial<Effect>): Effect {
	const { lower_bound: _lo, upper_bound: _hi, ...rest } = structuredClone(baseEffect);
	return { ...rest, ...overrides };
}

function render(verdict: Verdict, metricLabel?: string): string {
	return renderToStaticMarkup(<VerdictCard verdict={verdict} metricLabel={metricLabel} />);
}

describe("VerdictCard", () => {
	const cases: {
		name: string;
		verdict: Verdict;
		decisionText: string;
		tone: string;
		actionText: string;
		contains: string[];
		absent?: string[];
	}[] = [
		{
			name: "improved",
			verdict: verdictWith({
				decision: "improved",
				action: "ship",
				effect: effectWith({
					n_tasks: 8,
					n_nonzero: 7,
					n_positive: 7,
					n_negative: 0,
					d_bar: 0.3333,
					p_value: 0.015625,
					interval: [0.09, 0.57],
				}),
				tasks_needed: undefined,
			}),
			decisionText: ">IMPROVED<",
			tone: "good",
			actionText: "ACTION: SHIP",
			contains: [">+33.3 pp<", "approx. [+9, +57] pp", "7 of 8", "7 positive, 0 negative", ">0.0156<", "randomization unit: task"],
			absent: ['data-testid="verdict-stat-tasks-needed"'],
		},
		{
			name: "regressed",
			verdict: verdictWith({
				decision: "regressed",
				action: "hold",
				effect: effectWith({
					n_tasks: 8,
					n_nonzero: 6,
					n_positive: 0,
					n_negative: 6,
					d_bar: -0.25,
					p_value: 0.03125,
					interval: [-0.41, -0.09],
				}),
			}),
			decisionText: ">REGRESSED<",
			tone: "bad",
			actionText: "ACTION: HOLD",
			contains: [">-25 pp<", "0 positive, 6 negative", ">0.0313<", "Regressed on the primary endpoint or a guard failed."],
		},
		{
			name: "inconclusive (the fixture)",
			verdict: verdictFixture.verdict,
			decisionText: ">INCONCLUSIVE<",
			tone: "warn",
			actionText: "ACTION: NO DECISION",
			contains: [
				">+41.7 pp<",
				"approx. [-9.1, +92.5] pp",
				"3 of 4",
				">0.25<",
				"62.4 pp",
				"assumed SD 0.30",
				">7 tasks<",
				"No regression on clean",
				"No new integrity violation",
			],
		},
		{
			name: "invalid",
			verdict: verdictWith({
				decision: "invalid",
				action: "fix_harness",
				effect: undefined,
				invalid_reasons: ["graded-coverage: coverage 0.82 in arm B"],
				integrity: "",
			}),
			decisionText: ">INVALID<",
			tone: "neutral",
			actionText: "ACTION: FIX HARNESS",
			contains: [
				'data-testid="verdict-invalid-reasons"',
				"graded-coverage: coverage 0.82 in arm B",
				"An instrument gate fired: graded-coverage: coverage 0.82 in arm B.",
				">not assessed<",
				">not computed<",
			],
			absent: [">+41.7 pp<", 'Discordant tasks</dt><dd class="font-medium tabular-nums">3 of 4'],
		},
	];

	for (const c of cases) {
		it(`renders ${c.name}`, () => {
			const html = render(c.verdict);
			expect(html).toContain(`data-decision="${c.verdict.decision}"`);
			expect(html).toContain(c.decisionText);
			expect(html).toMatch(new RegExp(`data-testid="verdict-decision" data-tone="${c.tone}"`));
			expect(html).toContain(`data-action="${c.verdict.action}"`);
			expect(html).toContain(c.actionText);
			for (const text of c.contains) expect(html).toContain(text);
			for (const text of c.absent ?? []) expect(html).not.toContain(text);
		});
	}

	it("uses the catalog label for the primary metric", () => {
		expect(render(verdictFixture.verdict, "Verified success")).toContain("Primary: Verified success under stale-notes");
		expect(render(verdictFixture.verdict)).toContain("Primary: verified_success under stale-notes");
	});

	it("never shows integrity green beside a regression", () => {
		const regressedClean = render(verdictWith({ decision: "regressed", action: "hold", integrity: "clean" }));
		expect(regressedClean).toMatch(/data-integrity="clean" data-tone="neutral"/);
		const integrityRegressed = render(verdictWith({ decision: "improved", action: "hold", integrity: "regressed" }));
		expect(integrityRegressed).toMatch(/data-integrity="regressed" data-tone="bad"/);
		expect(integrityRegressed).toContain("Improved, but a guard failed or integrity regressed.");
		const improvedClean = render(verdictWith({ decision: "improved", action: "ship", integrity: "clean" }));
		expect(improvedClean).toMatch(/data-integrity="clean" data-tone="good"/);
	});

	it("says more than N tasks when tasks_needed_over is set", () => {
		const html = render(verdictWith({ tasks_needed: undefined, tasks_needed_over: 200 }));
		expect(html).toContain("more than 200 tasks");
	});

	it("shows the observed SD when the MDE does not assume one", () => {
		const html = render(verdictWith({ mde_assumed_sd: false, sd: 0.21 }));
		expect(html).toContain("SD 0.21");
		expect(html).not.toContain("assumed SD");
	});

	it("shows guard status and the interval it could not rule out", () => {
		const guard = structuredClone(verdictFixture.verdict.guards[0]);
		const effect = guard.effect as Effect;
		const verdict = verdictWith({
			decision: "inconclusive",
			guards: [
				{
					...guard,
					status: "undetermined",
					effect: { ...effect, lower_bound: { ...effect, d_bar: -0.1 }, upper_bound: { ...effect, d_bar: 0.2 } },
				},
				{ spec: { rule: "no_new_integrity_violation" }, status: "fail", detail: "1 new violation in arm B." },
			],
		});
		const html = render(verdict);
		expect(html).toMatch(/data-testid="verdict-guard-no_regression-clean" data-status="undetermined"/);
		expect(html).toContain(">UNDETERMINED<");
		expect(html).toContain("could not rule out -10 to +20 pp");
		expect(html).toContain("+8.3 pp, 1 discordant");
		expect(html).toMatch(/data-testid="verdict-guard-no_new_integrity_violation" data-status="fail"/);
		expect(html).toContain(">FAIL<");
		expect(html).toContain("1 new violation in arm B.");
	});

	it("prints tiny p-values as below the floor", () => {
		const html = render(verdictWith({ effect: effectWith({ p_value: 0.00001 }) }));
		expect(html).toContain("&lt; 0.0001");
	});

	it("has method notes on the p-value and the MDE", () => {
		const html = render(verdictFixture.verdict);
		expect(html).toContain('data-testid="verdict-method-pValue"');
		expect(html).toContain('data-testid="verdict-method-mde"');
	});
});