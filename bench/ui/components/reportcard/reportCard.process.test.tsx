import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReportCard } from "./reportCard";
import { scoreReport } from "./reportCard.fixtures";
import { formatProcess, hasProcess } from "./reportCard.utils";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("process scores", () => {
	it("formats path match, step ratio and repeats", () => {
		expect(formatProcess({ runs: 4, tool_match: 0.75, step_ratio: 1.4, redundant_calls: 0 })).toBe("Path match 75% · Steps 1.4x ref · Repeats 0");
		expect(formatProcess({ runs: 4, tool_match: null, step_ratio: 2, redundant_calls: null })).toBe("Steps 2.0x ref");
		expect(formatProcess({ runs: 0, tool_match: 1, step_ratio: 1, redundant_calls: 0 })).toBe("-");
		expect(formatProcess(undefined)).toBe("-");
	});
	it("shows only when an arm has process runs", () => {
		expect(hasProcess({ a: { process: { runs: 0, tool_match: null, step_ratio: null, redundant_calls: null } } })).toBe(false);
		expect(hasProcess({ a: undefined, b: { process: { runs: 2, tool_match: 1, step_ratio: 1, redundant_calls: 0 } } })).toBe(true);
	});
	it("renders on a benchmark row as report only", () => {
		const report = structuredClone(scoreReport);
		const row = report.benchmarks[0];
		const label = report.arms[0].label;
		row.arms[label] = { ...row.arms[label], process: { runs: 3, tool_match: 0.5, step_ratio: 1.2, redundant_calls: 1 } };
		const html = renderToStaticMarkup(<ReportCard report={report} />);
		expect(html).toContain("Process (report only)");
		expect(html).toContain("Path match 50%");
		expect(html).toContain("never change the score");
		expect(renderToStaticMarkup(<ReportCard report={scoreReport} />)).not.toContain("Process (report only)");
	});
});
