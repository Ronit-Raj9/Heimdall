import { describe, expect, it } from "vitest";
import type { Evaluation, Report } from "@/lib/store/apis/evaluationsApi";
import { evaluationTotals, rateText, reportCostUsd, reportRunCounts, totalCostText } from "./pageNumbers";
import { firstSolvedRun } from "./evaluationReportCard";
import { reportFaultText } from "@/components/releases/releaseEvidence";

const report = {
	evaluation: "ev_1",
	arms: [{ label: "A" }],
	capabilities: [],
	benchmarks: [{ benchmark: "b", capability: "coding", arms: { A: { score: 1, runs: 3 } } }],
	reliability: [{ fault: "model-down", arms: { A: { score: 2 / 3, drop: -0.33, runs: 3 } } }],
	totals: { A: { score: 1, ci_low: 0.03, ci_high: 1, runs: 3, solved: 3, cost_usd: 0.005, fault_runs: 3, fault_cost_usd: 0.0048 } },
} as unknown as Report;

describe("page numbers (Q-14, Q-15)", () => {
	it("counts every run, normal and with faults", () => {
		expect(reportRunCounts(report)).toEqual({ normal: 3, fault: 3, all: 6 });
		expect(reportCostUsd(report)).toBeCloseTo(0.0098);
	});
	it("subtitle and cost line share one spend: the benchmarks' spend wins", () => {
		const ev = { children: [{ benchmark: "b", experiment_id: "e", status: "done", spent_usd: 0.0098 }], result: { cost_usd: 0.005 } } as unknown as Evaluation;
		const t = evaluationTotals(ev, report);
		expect(t).toEqual({ runs: 6, costUsd: 0.0098 });
		expect(totalCostText(t)).toBe("$0.0098 for 6 runs");
	});
	it("under 10 runs a rate is a count with the same words", () => {
		expect(rateText({ score: 1, runs: 3, ci_low: 0.03, ci_high: 1 })).toBe("3 of 3, too few runs to rate");
		expect(rateText({ score: 0.5, runs: 20, ci_low: 0.3, ci_high: 0.7 })).toBe("50% (30%-70%)");
	});
	it("the release fault score reads the report's fault rows", () => {
		expect(reportFaultText(report, { label: "A", evaluation_id: "ev_1" })).toBe("2 of 3, too few runs to rate");
		expect(reportFaultText(report, { label: "A", evaluation_id: "other" })).toBeUndefined();
	});
	it("picks a solved normal run first", () => {
		expect(firstSolvedRun([{ run_id: "f", outcome: "fail", condition: "clean" }, { run_id: "x", outcome: "pass", condition: "c1" }, { run_id: "p", outcome: "pass", condition: "clean" }])).toBe("p");
	});
});
