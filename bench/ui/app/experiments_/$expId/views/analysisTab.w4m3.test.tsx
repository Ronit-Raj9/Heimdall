import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ANALYSIS_JOBS, AnalysisTab } from "./analysisTab";

const seen = vi.hoisted(() => ({ props: [] as { kind: string; confirm?: string }[] }));
vi.mock("@/components/jobs/jobRunButton", () => ({
	JobRunButton: (p: { kind: string; confirm?: string }) => {
		seen.props.push(p);
		return null;
	},
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: () => okQuery({ verdict: { decision: "improved", action: "ship" } }),
	useGetClustersQuery: () => okQuery({ clusters: [] }),
	useGetAgentsQuery: () => okQuery({ agents: [] }),
	useGetExperimentQuery: () => okQuery({ exp_id: "e_x", arms: ["A", "B"], multi_agent: true }),
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({ useGetAwarenessQuery: () => okQuery({ label: "", per_arm: {} }) }));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/analysisApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/analysisApi")>()),
	useAuditTableMutation: () => mutation(),
}));

describe("Analysis tab job buttons (p06-3)", () => {
	it("ask before starting every job", () => {
		seen.props = [];
		render(<AnalysisTab expId="e_x" />);
		expect(seen.props.length).toBeGreaterThanOrEqual(ANALYSIS_JOBS.length);
		for (const spec of ANALYSIS_JOBS) expect(seen.props.some((p) => p.kind === spec.kind)).toBe(true);
		const kinds = new Set(ANALYSIS_JOBS.map((s) => s.kind));
		for (const p of seen.props.filter((p) => kinds.has(p.kind as never)))
			expect(p.confirm).toMatch(/^Start .+ now\? It replaces the earlier result\.$/);
	});
});