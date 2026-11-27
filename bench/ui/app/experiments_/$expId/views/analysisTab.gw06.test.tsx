import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ANALYSIS_JOBS, AnalysisTab, summarizeAnalysisResult } from "./analysisTab";

const api = vi.hoisted(() => ({ experiment: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: () => okQuery({ verdict: { decision: "score", action: "no_decision" } }),
	useGetClustersQuery: () => okQuery({ clusters: [] }),
	useGetExperimentQuery: () => api.experiment,
	useGetAgentsQuery: () => okQuery({ agents: [] }),
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({
	useGetAwarenessQuery: () =>
		okQuery({
			label: "Heuristic phrase scan.",
			per_arm: { A: { runs: 30, runs_scanned: 28, runs_with_signals: 0, signals: 0 } },
			not_scanned: [
				{ run_id: "r_infra", reason: "no gateway log, no agent stream, no final message" },
				{ run_id: "r_quiet", reason: "no agent text" },
			],
		}),
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
const check = ANALYSIS_JOBS.find((s) => s.key === "check")!;
const blame = ANALYSIS_JOBS.find((s) => s.key === "blame")!;

beforeEach(() => {
	api.experiment = okQuery({ exp_id: "e_x", arms: ["A"], multi_agent: false });
});

describe("Re-check summary in plain words (N61)", () => {
	it("says a score run has no release decision", () => {
		const s = summarizeAnalysisResult(t, check, { status: "succeeded", result: { decision: "score", action: "no_decision" } });
		expect(s).toBe("Score saved. One setup, no release decision.");
	});
	it("names the verdict, the action and the rule set file", () => {
		const ok = summarizeAnalysisResult(t, check, {
			status: "succeeded",
			result: { decision: "improved", action: "ship", policy: "allow", policy_file: "default" },
		});
		expect(ok).toBe("B is better than A. Ready to release. Rule set default: allows release.");
		const bad = summarizeAnalysisResult(t, check, {
			status: "succeeded",
			result: { decision: "regressed", action: "hold", policy: "block", policy_file: "default" },
		});
		expect(bad).toBe("B is worse than A. Hold the release. Rule set default: blocks release.");
		expect(bad).not.toMatch(/regressed|no_decision|Verdict /);
	});
	it("keeps an unknown value as written, never a question mark", () => {
		const s = summarizeAnalysisResult(t, check, { status: "succeeded", result: { decision: "sideways", action: "wait" } });
		expect(s).toContain("sideways");
		expect(s).not.toContain("?");
	});
});

describe("Blame counts (N62)", () => {
	it("counts runs that were not scored apart from failed runs", () => {
		const s = summarizeAnalysisResult(t, blame, {
			status: "succeeded",
			result: { runs: 30, failed: 12, not_scored: 1, blamed: 0, skipped: 0, exp_id: "e_x" },
		});
		expect(s).toBe("30 runs: 12 failed, 1 not scored, 0 pinned on one agent");
		const none = summarizeAnalysisResult(t, blame, {
			status: "succeeded",
			result: { runs: 4, failed: 1, not_scored: 0, blamed: 1, skipped: 0, exp_id: "e_x" },
		});
		expect(none).toBe("4 runs: 1 failed, 1 pinned on one agent");
	});
});

describe("AnalysisTab on a one-setup score run (N61, N62, N63)", () => {
	it("shows the score sentence, lists unscanned runs and disables blame on single-agent setups", () => {
		const html = render(<AnalysisTab expId="e_x" />);
		expect(html).toContain("Score saved. One setup, no release decision.");
		expect(html).toContain("2 runs not scanned");
		expect(html).toContain('href="/runs/r_infra"');
		expect(html).toContain("no agent text");
		expect(html).toContain("One agent per setup. Blame is for team runs.");
		expect(tagOf(html, "job-run-blame.run")).toMatch(/\sdisabled=""/);
	});
	it("keeps blame available on a team run", () => {
		api.experiment = okQuery({ exp_id: "e_x", arms: ["A", "B"], multi_agent: true });
		const html = render(<AnalysisTab expId="e_x" />);
		expect(html).not.toContain("there is no blame to split");
		expect(tagOf(html, "job-run-blame.run")).not.toMatch(/\sdisabled=""/);
	});
});