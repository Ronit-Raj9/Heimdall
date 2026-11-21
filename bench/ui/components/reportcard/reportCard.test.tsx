import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport } from "./reportCard.fixtures";
import { capabilityRows, compareArms, duration, headline, overallScore, points, visibleReport } from "./reportCard.utils";

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
});