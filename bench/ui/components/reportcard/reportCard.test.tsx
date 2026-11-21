import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport } from "./reportCard.fixtures";
import { armDescription, armNames, capabilityRows, compareArms, costDisplay, duration, excludedText, headline, overallScore, points, visibleReport } from "./reportCard.utils";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
		<a href={to.replace("$expId", params?.expId ?? "")} {...rest}>
			{children}
		</a>
	),
}));

describe("report card utils", () => {
	it("weights the overall score by runs", () => {
		// (0.4*30 + 0.6*10) / 40 = 0.45
		expect(overallScore(scoreReport, "A")).toBeCloseTo(0.45);
		expect(headline(scoreReport).title).toBe("Your agent solves 45% overall");
		expect(headline({ ...scoreReport, mode: "score" }).title).toBe("Your agent scores 45% overall");
	});

	it("counts A/B wins from verdicts and CI overlap", () => {
		const h = headline(abReport);
		expect(h.title).toBe("codex v2 is better on 3 of 6 benchmarks");
		expect(h.detail).toBe("Overall codex v2 wins; worse on 1, tied on 2.");
	});

	it("prefers the server overall verdict", () => {
		const h = headline({ ...abReport, overall: { winner: "A", wins: { A: 4, B: 1, tie: 1 }, summary: "A is better on 4 of 6" } });
		expect(h.title).toBe("codex v1 is better on 4 of 6 benchmarks");
		expect(h.detail).toBe("A is better on 4 of 6");
	});

	it("names arms by agent, version, then model", () => {
		const arm = (label: string, agent: string, version?: number, model = "m") => ({ label, agent, version, model });
		expect(armNames(arm("A", "codex"), arm("B", "claude-code"))).toEqual(["codex", "claude-code"]);
		expect(armNames(arm("A", "codex", 1), arm("B", "codex", 2))).toEqual(["codex v1", "codex v2"]);
		expect(armNames(arm("A", "codex", undefined, "m1"), arm("B", "codex", undefined, "m2"))).toEqual(["codex on m1", "codex on m2"]);
		expect(armNames(arm("A", "codex"), arm("B", "codex"))).toEqual(["codex on m (A)", "codex on m (B)"]);
	});

	it("calls overlapping intervals a tie", () => {
		expect(compareArms(abReport.capabilities[1].arms, "A", "B")?.winner).toBe("tie");
		expect(compareArms(abReport.capabilities[0].arms, "A", "B")).toMatchObject({
			winner: "B",
			significant: true,
		});
	});

	it("lists the seven fixed capability rows first", () => {
		const rows = capabilityRows({
			...scoreReport,
			capabilities: [...scoreReport.capabilities, { capability: "gpu", benchmarks: [], arms: {} }],
		});
		expect(rows.map((r) => r.capability)).toEqual(["terminal", "coding", "memory", "support", "research", "voice", "safety", "gpu"]);
	});

	it("formats points and durations", () => {
		expect(points(-0.21)).toBe("-21 pts");
		expect(points(0.08)).toBe("+8 pts");
		expect(duration(410)).toBe("6m 50s");
	});
});

describe("excluded runs", () => {
	it("names out of memory first in plain words", () => {
		expect(excludedText({ infra_error: 1, out_of_memory: 2, invalid_evaluator: 0 })).toBe("excluded: 2 out of memory, 1 infrastructure error");
		expect(excludedText(undefined)).toBe("");
		expect(excludedText({ unknown: 0 })).toBe("");
	});

	it("shows excluded counts and the held-out split on the card", () => {
		const bench = scoreReport.benchmarks[0];
		const label = scoreReport.arms[0].label;
		const report = {
			...scoreReport,
			split: "held_out",
			benchmarks: [{ ...bench, arms: { ...bench.arms, [label]: { ...bench.arms[label], excluded: { out_of_memory: 3 } } } }, ...scoreReport.benchmarks.slice(1)],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain("Excluded: 3 out of memory. Not counted in the score.");
		expect(html).toContain("Held-out tasks only");
	});
});

describe("ReportCard", () => {
	it("renders the score-mode headline, table, reliability and safety", () => {
		const html = renderToStaticMarkup(<ReportCard report={scoreReport} />);
		expect(html).toContain("Your agent solves 45% overall");
		expect(html).toContain("codex v2 on gpt-6.2-sol");
		expect(html).toContain('data-testid="capability-terminal"');
		expect(html).toContain("Not tested");
		expect(html).toContain("$3.20");
		expect(html).toContain("123.5k");
		expect(html).toContain("Model goes down");
		expect(html).toContain("-21 pts");
		expect(html).toContain("Attacks resisted");
		expect(html).toContain("88%");
		expect(html).toContain('href="/experiments/exp_tb2"');
		expect(html).toContain("Download JSON");
		expect(html).toContain("Copy link");
		expect(html).not.toContain("winner-mark");
	});

	it("renders A/B deltas with a text winner marker", () => {
		const html = renderToStaticMarkup(<ReportCard report={abReport} />);
		expect(html).toContain("codex v2 is better on 3 of 6 benchmarks");
		expect(html).toContain("Change (B vs A)");
		expect(html).toContain("+30 pts");
		expect(html).toContain("codex v2 better");
		expect(html).toContain("codex v1 better");
		expect(html).toContain("No clear winner");
		expect(html).toContain("codex v2 solves 30 points more");
		expect(html).toContain("A drop");
		expect(html).toContain('href="/experiments/exp_6"');
	});

	it("says when practice runs were left out", () => {
		const note = "6 practice runs were left out: the scripted practice model answered them, so they are not findings.";
		const html = renderToStaticMarkup(<ReportCard report={{ ...scoreReport, practice_runs: 6, note }} />);
		expect(html).toContain('data-testid="report-card-practice"');
		expect(html).toContain(note);
		expect(renderToStaticMarkup(<ReportCard report={scoreReport} />)).not.toContain("report-card-practice");
	});

	it("renders the safety and alignment section with intervals and not tested", () => {
		const html = renderToStaticMarkup(<ReportCard report={abReport} />);
		expect(html).toContain("Safety and alignment");
		expect(html).toContain("Destructive actions");
		expect(html).toContain("25% (3-65%)");
		expect(html).toContain("2 of 8 runs");
		expect(html).toContain("Prompt injection");
		expect(html).toContain("Not tested");
		expect(html).toContain("Impossible tasks flagged");
		expect(html).toContain("1 of 2 risks tested");
		expect(html).toContain("ASI02 Tool Misuse and Exploitation");
		expect(html).not.toMatch(/probe|gate|canary/i);
	});

	it("keeps only the asked arm's safety rates in score mode", () => {
		const v = visibleReport({ ...abReport, mode: "score" });
		expect(Object.keys(v.alignment!.categories[0].arms)).toEqual(["A"]);
		expect(v.alignment!.impossible_flagged).toEqual({});
	});

	it("renders the reliability and resilience cards with unknowns as a dash", () => {
		const curve = [1, 2, 3].map((k) => ({
			k,
			tasks: 4,
			pass_at_k: 0.5 + 0.1 * k,
			pass_at_k_low: 0.4,
			pass_at_k_high: 0.9,
			pass_hat_k: 0.6 - 0.15 * k,
			pass_hat_k_low: 0,
			pass_hat_k_high: 0.6,
		}));
		const report = {
			...scoreReport,
			benchmarks: [
				{
					...scoreReport.benchmarks[0],
					reliability_card: {
						A: {
							k: 3,
							tasks: 4,
							pass1: 0.6,
							pass_at_k: 0.8,
							pass_hat_k: 0.15,
							curve,
							consistency: 0.42,
							flip_rate: 0.5,
							cost_per_solved: null,
						},
					},
				},
			],
			resilience: [
				{
					fault: "tool-rate-limit",
					benchmarks: ["hd-harbor-tb2"],
					arms: {
						A: {
							tasks: 4,
							success_clean: 0.6,
							success_fault: 0.45,
							recovery_rate: 0.7,
							degradation_ratio: 0.75,
							degradation_ratio_low: 0.5,
							degradation_ratio_high: 0.95,
							cost_inflation: 1.4,
							cost_inflation_low: 1.1,
							cost_inflation_high: 1.8,
							time_to_recover_s: 12,
							recovered_share: null,
							retry_after_respected: null,
						},
					},
				},
			],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain('data-testid="reliability-chart-hd-harbor-tb2-A"');
		expect(html).toContain("flakiness");
		expect(html).toContain("Solved in all 3 tries");
		expect(html).toMatch(/reliability-consistency-hd-harbor-tb2-A">42%/);
		expect(html).toMatch(/reliability-flip-hd-harbor-tb2-A">50%/);
		expect(html).toMatch(/reliability-cost-hd-harbor-tb2-A">-</);
		expect(html).toContain("Tool rate limit");
		expect(html).toMatch(/resilience-ratio-tool-rate-limit-A">x0.75/);
		expect(html).toContain("x0.50 to x0.95");
		expect(html).toContain("x1.40");
		expect(html).toContain("12s");
		expect(html).not.toContain(" arm");
	});
});

describe("learning and persistence sections", () => {
	it("hides both sections without data", () => {
		const html = renderToStaticMarkup(<ReportCard report={scoreReport} />);
		expect(html).not.toContain("Learning over time");
		expect(html).not.toContain("Persistence");
	});

	it("shows the learning curve and persistence numbers when present", () => {
		const report = {
			...scoreReport,
			learning: [
				{
					benchmark: "coding-core",
					arm: "A",
					aulc: 0.62,
					final_gain: 0.25,
					bwt: -0.1,
					fwt: 0.15,
					episodes_to_threshold: 4,
					threshold: 0.8,
					curve: [
						{ index: 1, success: 0.3, runs: 2 },
						{ index: 2, success: 0.6, runs: 2 },
						{ index: 3, success: 0.9, runs: 2 },
					],
					chains: 2,
				},
			],
			persistence: [{ arm: "A", resumed_without_redo: { value: 0.5, runs: 4 }, graceful_finish: { value: 1, runs: 2 }, cost_normalized_success: { value: 4.2, runs: 6 } }],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain("Learning over time");
		expect(html).toContain("learning-sparkline");
		expect(html).toContain("Persistence");
		expect(html).toContain("4.2 per $");
		expect(html).not.toMatch(/—|–/);
	});

	it("names the no-memory control the gain from memory compares with", () => {
		const row = { benchmark: "coding-core", arm: "A", aulc: 0.6, final_gain: 0.2, bwt: 0, fwt: 0.1, episodes_to_threshold: null, threshold: 0.8, curve: [], chains: 1 };
		const html = renderToStaticMarkup(<ReportCard report={{ ...scoreReport, learning: [{ ...row, control: "codex v2 on m (no memory)" }] }} />);
		expect(html).toContain('data-testid="learning-control"');
		expect(html).toContain("codex v2 on m (no memory)");
		expect(renderToStaticMarkup(<ReportCard report={{ ...scoreReport, learning: [row] }} />)).not.toContain("learning-control");
	});
});

describe("unpriced cost (N2)", () => {
	it("never shows $0 for an unpriced model: not known, with the count and reason", () => {
		const c = costDisplay({ runs: 8, cost_usd: null, cost_runs: 5, cost_note: "1 of 1 cost rows unpriced" });
		expect(c).toMatchObject({ text: "Not known", known: false, detail: "3 of 8 runs unpriced" });
		expect(c.reason).toBe("Cost not known, 3 of 8 runs unpriced: 1 of 1 cost rows unpriced");
		expect(costDisplay({ runs: 8, cost_usd: 1.5, cost_runs: 8 }).text).toBe("$1.50");
		expect(costDisplay({ runs: 0, cost_usd: null }).text).toBe("-");
	});

	it("renders Not known in the card, never $0.00", () => {
		const unpriced = { ...scoreReport.benchmarks[0].arms.A, cost_usd: null, cost_runs: 0, cost_note: "no price for model gpt-x", tokens: null };
		const report = {
			...scoreReport,
			capabilities: [{ ...scoreReport.capabilities[0], arms: { A: unpriced } }],
			benchmarks: [{ ...scoreReport.benchmarks[0], arms: { A: unpriced } }],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain('data-testid="cost-not-known"');
		expect(html).toContain("30 of 30 runs unpriced");
		expect(html).toContain("no price for model gpt-x");
		expect(html).not.toContain("$0.00");
	});

	it("names the version that was latest at launch", () => {
		expect(armDescription({ label: "A", agent: "codex", version: 3, latest_at_launch: true, model: "m" })).toBe("codex v3 (latest at launch) on m");
		expect(armDescription({ label: "A", agent: "codex", version: 3, model: "m" })).toBe("codex v3 on m");
	});
});
