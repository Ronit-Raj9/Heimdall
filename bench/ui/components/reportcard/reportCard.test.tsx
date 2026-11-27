import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport } from "./reportCard.fixtures";
import {
	armDescription,
	armNames,
	capabilityName,
	capabilityRows,
	compareArms,
	costDisplay,
	duration,
	excludedText,
	downloadReportJSON,
	headline,
	overallScore,
	points,
	visibleReport,
} from "./reportCard.utils";

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
		// Without fault rows the headline is the plain score (M3-3 adds a fault split when faults ran).
		const plain = { ...scoreReport, reliability: [] };
		expect(headline(plain).title).toBe("Your agent solves 45% overall");
		expect(headline({ ...plain, mode: "score" }).title).toBe("Your agent scores 45% overall");
	});

	it("counts A/B wins from verdicts and CI overlap", () => {
		const h = headline(abReport);
		expect(h.title).toBe("codex v2 is better on 3 of 6 benchmarks");
		expect(h.detail).toBe("Overall codex v2 wins; worse on 1, tied on 2.");
	});

	it("uses the tie wording when the server winner is unclear (D8)", () => {
		for (const winner of ["unclear", "", "nobody"]) {
			const h = headline({ ...abReport, overall: { winner, wins: { A: 0, B: 0, tie: 1 }, summary: "" } });
			expect(h.title).toBe("No clear winner across 6 benchmarks");
			expect(h.title).not.toContain("unclear");
			expect(h.detail).toBe("Neither agent is clearly better overall.");
		}
	});

	it("names the support capability for tool use too (A4.cap)", () => {
		expect(capabilityName("support")).toBe("Support and tool use");
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
		// One duration format everywhere (M3-24).
		expect(duration(410)).toBe("6 min 50 s");
	});
});

describe("excluded runs", () => {
	it("names out of memory first in plain words", () => {
		expect(excludedText({ infra_error: 1, out_of_memory: 2, invalid_evaluator: 0 })).toBe(
			"excluded: 2 out of memory, 1 infrastructure error",
		);
		expect(excludedText(undefined)).toBe("");
		expect(excludedText({ unknown: 0 })).toBe("");
	});

	it("shows excluded counts and the held-out split on the card", () => {
		const bench = scoreReport.benchmarks[0];
		const label = scoreReport.arms[0].label;
		const report = {
			...scoreReport,
			split: "held_out",
			benchmarks: [
				{ ...bench, arms: { ...bench.arms, [label]: { ...bench.arms[label], excluded: { out_of_memory: 3 } } } },
				...scoreReport.benchmarks.slice(1),
			],
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
		expect(html).toContain("A change from normal");
		expect(html).toContain('href="/experiments/exp_6"');
	});

	it("says when practice runs were left out", () => {
		const note = "6 practice runs were left out: the scripted practice model answered them, so they are not findings.";
		const html = renderToStaticMarkup(<ReportCard report={{ ...scoreReport, practice_runs: 6, note }} />);
		expect(html).toContain('data-testid="report-card-practice"');
		expect(html).toContain(note);
		expect(renderToStaticMarkup(<ReportCard report={scoreReport} />)).not.toContain("report-card-practice");
	});

	it("shows the MCP route when it is not direct", () => {
		const html = renderToStaticMarkup(<ReportCard report={{ ...scoreReport, mcp_route: "bifrost_code_mode" }} />);
		expect(html).toContain('data-testid="report-card-mcp-route"');
		expect(html).toContain("MCP tools through Bifrost, Code Mode");
		expect(renderToStaticMarkup(<ReportCard report={{ ...scoreReport, mcp_route: "direct" }} />)).not.toContain("report-card-mcp-route");
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
		expect(html).toContain("Covers 1 of 2 OWASP Top 10 agent risks tested");
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
		expect(html).toContain("12 s");
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
			persistence: [
				{
					arm: "A",
					resumed_without_redo: { value: 0.5, runs: 4 },
					graceful_finish: { value: 1, runs: 2 },
					cost_normalized_success: { value: 4.2, runs: 6 },
				},
			],
		};
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain("Learning over time");
		expect(html).toContain("learning-sparkline");
		expect(html).toContain("Persistence");
		// Success per dollar is dropped; cost per solved task is the one cost metric (M3-13).
		expect(html).not.toContain("4.2 per $");
		expect(html).not.toMatch(/—|–/);
	});

	it("names the no-memory control the gain from memory compares with", () => {
		const row = {
			benchmark: "coding-core",
			arm: "A",
			aulc: 0.6,
			final_gain: 0.2,
			bwt: 0,
			fwt: 0.1,
			episodes_to_threshold: null,
			threshold: 0.8,
			curve: [],
			chains: 1,
		};
		const html = renderToStaticMarkup(
			<ReportCard report={{ ...scoreReport, learning: [{ ...row, control: "codex v2 on m (no memory)" }] }} />,
		);
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
		const unpriced = {
			...scoreReport.benchmarks[0].arms.A,
			cost_usd: null,
			cost_runs: 0,
			cost_note: "no price for model gpt-x",
			tokens: null,
		};
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
		expect(armDescription({ label: "A", agent: "codex", version: 3, latest_at_launch: true, model: "m" })).toBe(
			"codex v3 (latest at launch) on m",
		);
		expect(armDescription({ label: "A", agent: "codex", version: 3, model: "m" })).toBe("codex v3 on m");
	});
});

describe("report exports (A11)", () => {
	it("offers HTML, Markdown, PDF and the reproduce kit next to JSON", () => {
		const html = renderToStaticMarkup(<ReportCard report={abReport} />);
		expect(html).toContain('data-testid="report-download-json"');
		expect(html).toContain('/evaluations/ev_ab1/report/export?format=html"');
		expect(html).toContain('/evaluations/ev_ab1/report/export?format=md"');
		expect(html).toContain('/evaluations/ev_ab1/reproduce"');
		expect(html).toContain('data-testid="report-download-pdf"');
		expect(html).toContain("Download HTML");
		expect(html).toContain("Download Markdown");
		expect(html).toContain("Save as PDF");
		expect(html).toContain("Download reproduce kit");
	});

	it("hides the server exports when the report has no evaluation id", () => {
		const html = renderToStaticMarkup(<ReportCard report={{ ...abReport, evaluation: "" }} />);
		expect(html).toContain('data-testid="report-download-json"');
		expect(html).not.toContain("report/export");
		expect(html).not.toContain('data-testid="report-download-pdf"');
	});
});
describe("fault table (N57)", () => {
	it("heads the change column 'Change from normal' and says 'no change' for 0", () => {
		const report = { ...scoreReport, reliability: [{ fault: "model-down", arms: { A: { score: 0.56, drop: 0, runs: 9 } } }] };
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain("Change from normal");
		expect(html).not.toContain(">Drop<");
		expect(html).toContain("no change");
		expect(html).toContain("how it changed from a normal run of the same tasks");
		expect(html).not.toContain("report-reliability-unpaired");
	});

	it("says how many fault runs were left out", () => {
		const one = { ...scoreReport, reliability: [{ fault: "model-down", arms: { A: { score: 0.56, drop: 0, runs: 9, unpaired: 1 } } }] };
		expect(renderToStaticMarkup(<ReportCard report={one} />)).toContain(
			"1 run under faults was left out: its task has no finished normal run to compare with.",
		);
		const three: typeof abReport = {
			...abReport,
			reliability: [
				{ fault: "broken-tool", arms: { A: { score: 0.1, drop: -0.3, unpaired: 2 }, B: { score: 0.4, drop: -0.15 } } },
				{ fault: "model-down", arms: { B: { score: 0.4, drop: 0.04, unpaired: 1 } } },
			],
		};
		const html = renderToStaticMarkup(<ReportCard report={three} />);
		expect(html).toContain("3 runs under faults were left out: their task has no finished normal run to compare with.");
		expect(html).toContain("+4 pts");
	});
});

describe("Download JSON (N59)", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it("revokes the object URL only after the timer runs", () => {
		vi.useFakeTimers();
		const anchor = { href: "", download: "", click: vi.fn(), remove: vi.fn() };
		vi.stubGlobal("document", { createElement: () => anchor, body: { appendChild: vi.fn() } });
		vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
		const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
		downloadReportJSON({ ...abReport, evaluation: "ev_x" });
		expect(anchor.download).toBe("report-card-ev_x.json");
		expect(anchor.click).toHaveBeenCalledOnce();
		expect(revoke).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1000);
		expect(revoke).toHaveBeenCalledWith("blob:x");
	});
});
describe("ReportCard thrown-out runs (fix wave 3, N215)", () => {
	const empty = { score: 0, ci_low: 0, ci_high: 0, runs: 0, cost_usd: null, tokens: null, steps: 0, tool_calls: 0, wall_s: 0 };
	const thrown = {
		...scoreReport,
		status: "done",
		mode: "score",
		capabilities: [{ capability: "support", benchmarks: ["hd-harbor-bfcl"], arms: { A: empty } }],
		benchmarks: [
			{
				benchmark: "hd-harbor-bfcl",
				capability: "support",
				experiment_id: "e_eval_5rcc",
				arms: { A: empty },
				thrown_out: { A: { runs: 24, checks: { model_fidelity: 24 }, reasons: ["gate model_fidelity fired in 24 of 24 runs"] } },
			},
		],
		reliability: [],
		thrown_out_runs: 24,
	};

	it("shows no score, the thrown-out row with its check and a link to the comparison", () => {
		const html = renderToStaticMarkup(<ReportCard report={thrown} />);
		expect(html).toContain("No score: every run was thrown out by a safety check");
		expect(html).toContain(
			"Thrown out: 24 runs failed the model check (the provider answered with a different model than the one asked for). Not counted in the score.",
		);
		expect(html).toContain('data-testid="report-card-thrown-out-link"');
		expect(html).toContain('href="/experiments/e_eval_5rcc"');
		expect(html).not.toContain(">0%<");
	});
});