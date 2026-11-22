import { evaluationReportUrl } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { abReport, scoreReport, slicedReport } from "./reportCard.fixtures";
import { ReportFilterBar, ReportSlices, whereText } from "./reportSlices";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, params: _params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

describe("ReportSlices", () => {
	it("is hidden with no segments", () => {
		expect(render(<ReportSlices report={abReport} />)).toBe("");
		expect(render(<ReportCard report={scoreReport} />)).not.toContain("By segment");
	});

	it("shows each value with tasks, scores, intervals, change and the lowest badge", () => {
		const html = render(<ReportSlices report={slicedReport} onShowOnly={() => undefined} />);
		expect(html).toContain("By segment");
		expect(html).toContain('data-testid="slice-key-tier"');
		expect(html).toContain('data-testid="slice-key-region"');
		expect(html).toContain('data-testid="slice-tier-enterprise"');
		expect(html).toContain("70%");
		expect(html).toContain("55%-82%");
		expect(html).toContain("+20 pts");
		expect(html).toContain("-5 pts");
		expect(html.match(/>Lowest</g)).toHaveLength(2);
		expect(html).toContain("Too few tasks to rank");
		expect(html).toContain("3 more values not listed.");
		expect(html).toContain('data-testid="slice-only-enterprise"');
	});

	it("never marks a small segment lowest and has no filter button in a shared view", () => {
		const report = { ...slicedReport, slices: [{ ...slicedReport.slices![0], worst: { A: "trial" } }] };
		const html = render(<ReportSlices report={report} />);
		expect(html).not.toContain(">Lowest<");
		expect(html).not.toContain("Show only this segment");
	});

	it("uses only plain words", () => {
		const html = render(<ReportCard report={slicedReport} onWhere={() => undefined} />).toLowerCase();
		for (const w of [" arm ", "condition", "probe", "canary", "sealed", "lease", "suite", "runner", "—", "–"]) expect(html).not.toContain(w);
	});
});

describe("ReportFilterBar", () => {
	it("says what the report is narrowed to, with a clear button", () => {
		expect(whereText({ tier: "enterprise", region: "eu" })).toBe("tier = enterprise and region = eu");
		const html = render(<ReportFilterBar where={{ tier: "enterprise" }} onClear={() => undefined} />);
		expect(html).toContain("Showing tasks with");
		expect(html).toContain("tier = enterprise");
		expect(html).toContain('data-testid="report-filter-clear"');
		expect(render(<ReportFilterBar />)).toBe("");
	});

	it("shows on the report card when the report has a filter", () => {
		const html = render(<ReportCard report={slicedReport} onWhere={() => undefined} />);
		expect(html).toContain('data-testid="report-filter"');
		expect(html).toContain("region = eu");
	});
});

describe("evaluationReportUrl", () => {
	it("adds one where param per filter", () => {
		expect(evaluationReportUrl("ev 1")).toBe("/evaluations/ev%201/report");
		expect(evaluationReportUrl({ id: "ev1", where: { tier: "enterprise", region: "eu" } })).toBe(
			"/evaluations/ev1/report?where=tier%3Aenterprise&where=region%3Aeu",
		);
		expect(evaluationReportUrl({ id: "ev1" })).toBe("/evaluations/ev1/report");
	});
});
