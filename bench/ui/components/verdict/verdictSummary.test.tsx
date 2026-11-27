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
		// The setup letter alone when it has no other name: never "B (B)" (fix wave 3, N244).
		expect(html).toMatch(new RegExp(`>${arms[1]} is better by [0-9.]+ pp<`));
		expect(html).not.toContain("B (B)");
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

describe("VerdictSummary setups, guard and running headlines (fix wave 3, N244, N200, N197)", () => {
	const head = (html: string) => html.match(/data-testid="verdict-summary-headline"[^>]*>([^<]*)</)?.[1];
	const guardFail = {
		spec: { rule: "no_new_integrity_violation" },
		status: "fail" as const,
		detail: "veto gate fired in arm B on t02 where arm A has none",
	};
	// The live comparison e_zz_test_matrix3_6z3h after a Re-check: three setups, B broke a safety rule on t02.
	const setups = {
		leader: "A",
		top_group: ["A", "C", "B"],
		behind: [],
		separable: false,
		note: "Cannot separate any two arms on this suite: with at most 2 common tasks the smallest possible p is 0.5.",
		pairs: [],
		safety: { B: ["t02"] },
	};
	const three = (verdict: Parameters<typeof verdictWith>[0], props: Partial<Parameters<typeof VerdictSummary>[0]> = {}) =>
		renderToStaticMarkup(
			<VerdictSummary
				verdict={verdictWith(verdict)}
				arms={["A", "B", "C"]}
				naive={{ A: { num: 2, den: 2, unknown: 0 }, B: { num: 1, den: 2, unknown: 0 }, C: { num: 2, den: 2, unknown: 0 } }}
				{...props}
			/>,
		);

	it("says three setups cannot be told apart, names the safety break and shows every setup", () => {
		const html = three({ decision: "inconclusive", action: "no_decision", setups });
		expect(head(html)).toBe("Cannot tell A, B and C apart yet");
		expect(html).toContain("B broke a safety rule on t02.");
		expect(html).toContain("Cannot separate any two arms");
		expect(html).not.toContain("is worse by");
		for (const arm of ["A", "B", "C"]) expect(html).toContain(`data-testid="verdict-summary-arm-${arm}"`);
	});

	it("names a leader and tells to use it, or to hold off when it broke a safety rule", () => {
		const names = { A: "codex on gpt-6-luna", B: "codex on gpt-6.2-sol", C: "codex on gpt-5.6-terra" };
		const leader = { ...setups, top_group: ["A"], behind: ["B", "C"], separable: true, safety: {} };
		const clean = three({ decision: "leader", action: "no_decision", setups: leader }, { names });
		expect(head(clean)).toBe("A (codex on gpt-6-luna) is ahead of every other setup");
		expect(clean).toContain("Use A (codex on gpt-6-luna).");
		const flagged = three({ decision: "leader", action: "hold", setups: { ...leader, safety: { A: ["t01", "t03"] } } }, { names });
		expect(flagged).toContain("Hold off: A (codex on gpt-6-luna) broke a safety rule on 2 tasks.");
		expect(flagged).not.toContain("Use A");
	});

	it("says a guard broke the rule, never worse by, when no effect was significant", () => {
		const html = render({ decision: "regressed", action: "hold", basis: "guard", guards: [guardFail] });
		expect(head(html)).toBe("B broke a safety rule on t02");
		expect(html).not.toContain("worse by");
		expect(html).toContain("Hold off. A safety rule or safety check failed");
	});

	it("prints the setup's name next to its letter", () => {
		const html = renderToStaticMarkup(
			<VerdictSummary
				verdict={verdictWith({ decision: "regressed", action: "hold", basis: "guard", guards: [guardFail] })}
				arms={["A", "B"]}
				names={{ A: "codex on gpt-6-luna", B: "codex on gpt-6.2-sol" }}
				naive={{}}
			/>,
		);
		expect(head(html)).toBe("B (codex on gpt-6.2-sol) broke a safety rule on t02");
	});

	it("names why a test was broken", () => {
		const html = render({
			decision: "invalid",
			action: "fix_harness",
			invalid_reasons: ["gate model_fidelity fired in 3 of 3 runs (100.0%) of arm B, above 5%"],
		});
		expect(head(html)).toBe(
			"Test was broken: the model gateway answered 3 of 3 runs of setup B with a different model than the one asked for (model check). This is not the agent&#x27;s fault; fix it and press Re-check.",
		);
	});

	it("says still running instead of any decision while the comparison runs", () => {
		const html = renderToStaticMarkup(
			<VerdictSummary
				verdict={verdictWith({ decision: "invalid", action: "fix_harness" })}
				arms={arms}
				naive={{}}
				partial
				onNext={() => undefined}
			/>,
		);
		expect(head(html)).toBe("Still running: no result yet");
		expect(html).not.toContain("Test was broken");
		expect(html).not.toContain('data-testid="verdict-summary-next-button"');
	});
});
describe("VerdictSummary much cheaper at the same score (M4-1, M4-2, M4-12)", () => {
	const obs = (v: number) => ({
		state: "observed" as const,
		v,
		evidence: "gateway" as const,
	});
	const naive = {
		A: { num: 7, den: 8, unknown: 0 },
		B: { num: 7, den: 8, unknown: 0 },
	};
	const html = renderToStaticMarkup(
		<VerdictSummary
			verdict={verdictWith({ decision: "inconclusive", action: "no_decision" })}
			arms={["A", "B"]}
			naive={naive}
			armMetrics={{ A: { cost_usd: obs(0.095) }, B: { cost_usd: obs(0.0062) } }}
			armCost={{ A: obs(0.104), B: obs(0.0068) }}
		/>,
	);
	it("says B costs 15 times less and gives both pass counts and costs", () => {
		expect(html).toContain("No difference in score. B costs 15 times less.");
		expect(html).toContain("A solved 7 of 8 at $0.10 a run. B solved 7 of 8 at $0.0068 a run.");
		expect(html).toContain('data-testid="verdict-summary-cheaper-B"');
		expect(html).not.toContain('data-testid="verdict-summary-cheaper-A"');
	});
	it("labels the mean as the average cost and the median as a second value", () => {
		expect(html).toContain("Average cost per run");
		expect(html).toContain("Median $0.095");
	});
	it("is a grey panel, not a warning", () => {
		expect(html).not.toContain("chart-warning");
	});
	it("keeps no clear winner when the costs are close", () => {
		const close = renderToStaticMarkup(
			<VerdictSummary
				verdict={verdictWith({
					decision: "inconclusive",
					action: "no_decision",
				})}
				arms={["A", "B"]}
				naive={naive}
				armCost={{ A: obs(0.1), B: obs(0.08) }}
			/>,
		);
		expect(close).toContain("No clear winner yet");
	});
});