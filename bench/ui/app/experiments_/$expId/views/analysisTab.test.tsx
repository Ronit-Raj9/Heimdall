import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { auditRequest } from "@/lib/store/apis/analysisApi";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ANALYSIS_JOBS, AnalysisTab, analysisJobParams, summarizeAnalysisResult } from "./analysisTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: () => okQuery({ verdict: { decision: "improved", action: "ship" } }),
	useGetClustersQuery: () => okQuery({ clusters: [{ signature: "sig_timeout", title: "Agent timed out", count: 3 }] }),
	useGetAgentsQuery: () => okQuery({ agents: [{ id: "stub", path: "agents/stub.yaml" }] }),
	useGetExperimentQuery: () => okQuery({ exp_id: "e_x", arms: ["A", "B"], multi_agent: true }),
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({
	useGetAwarenessQuery: () =>
		okQuery({ label: "Heuristic phrase scan.", per_arm: { A: { runs: 4, runs_scanned: 4, runs_with_signals: 1, signals: 2 } } }),
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/analysisApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/analysisApi")>()),
	useAuditTableMutation: () => mutation(),
}));

const t = i18n.getFixedT("en", "analysis");

describe("AnalysisTab", () => {
	it("offers one job button per analysis kind and shows the refreshed views", () => {
		const html = render(<AnalysisTab expId="e_x" />);
		for (const spec of ANALYSIS_JOBS) expect(html).toContain(`data-testid="job-run-${spec.kind}"`);
		expect(html).toContain("B is better than A. Ready to release.");
		expect(html).toContain("Setup A: 1 of 4 scanned runs with signals (2 signals)");
		expect(html).toContain("sig_timeout");
		expect(html).toContain('data-testid="analysis-link-cost-explain"');
		expect(html).toContain('data-testid="analysis-link-pareto"');
		expect(html).toContain('data-testid="analysis-audit-submit"');
		// tier-2 blame replay sits on the blame card
		expect(html).toContain('data-testid="job-run-blame.replay"');
		expect(html).toContain('data-testid="blame-replay-stub"');
	});
	it("passes exp_id only to the per-experiment kinds", () => {
		const check = ANALYSIS_JOBS.find((s) => s.kind === "check.run")!;
		const clusters = ANALYSIS_JOBS.find((s) => s.kind === "clusters.rebuild")!;
		expect(analysisJobParams(check, "e_x")).toEqual({ exp_id: "e_x" });
		expect(analysisJobParams(clusters, "e_x")).toEqual({});
	});
	it("summarizes a finished job's result and a failure's error", () => {
		const codescan = ANALYSIS_JOBS.find((s) => s.kind === "codescan.run")!;
		const ok = summarizeAnalysisResult(t, codescan, {
			status: "succeeded",
			result: { findings: 2, scanned: 3, runs: 4, vetoed: ["r_1"], by_severity: {}, skipped: 0, exp_id: "e_x" },
		});
		expect(ok).toBe("2 counted findings over 3 of 4 runs; 1 would be vetoed");
		expect(summarizeAnalysisResult(t, codescan, { status: "failed", error: "experiment e_x: not found" })).toBe("experiment e_x: not found");
		expect(summarizeAnalysisResult(t, codescan, { status: "running" })).toBe("");
	});
	it("posts an audit table as CSV", () => {
		expect(auditRequest("task,a_pass,a_n,b_pass,b_n\n")).toMatchObject({ url: "/audit", method: "POST", headers: { "Content-Type": "text/csv" } });
	});
});
