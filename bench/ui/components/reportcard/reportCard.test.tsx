import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport } from "./reportCard.fixtures";
import { armDescription, capabilityRows, compareArms, costDisplay, duration, headline, overallScore, points } from "./reportCard.utils";

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
	});

	it("counts A/B wins from verdicts and CI overlap", () => {
		const h = headline(abReport);
		expect(h.title).toBe("B is better on 3 of 6 benchmarks");
		expect(h.detail).toBe("Overall B wins; worse on 1, tied on 2.");
	});

	it("prefers the server overall verdict", () => {
		const h = headline({ ...abReport, overall: { winner: "A", wins: { A: 4, B: 1, tie: 1 }, summary: "A is better on 4 of 6" } });
		expect(h.title).toBe("A is better on 4 of 6 benchmarks");
		expect(h.detail).toBe("A is better on 4 of 6");
	});

	it("calls overlapping intervals a tie", () => {
		expect(compareArms(abReport.capabilities[1].arms, "A", "B")?.winner).toBe("tie");
		expect(compareArms(abReport.capabilities[0].arms, "A", "B")).toMatchObject({ winner: "B", significant: true });
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
		expect(html).toContain("B is better on 3 of 6 benchmarks");
		expect(html).toContain("Change (B vs A)");
		expect(html).toContain("+30 pts");
		expect(html).toContain("B better");
		expect(html).toContain("A better");
		expect(html).toContain("No clear winner");
		expect(html).toContain("B solves 30 points more");
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
