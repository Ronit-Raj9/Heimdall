import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

// Decision 1, the lead's rule 14: with "Show original result" picked (?grade=original), the analysis,
// actions and matrix tabs read the original too, not the newest re-score. The page passes the grade
// source it shows to each tab, and each tab asks the verdict for it.

const seen = vi.hoisted(() => ({ verdict: [] as unknown[], matrix: [] as unknown[] }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetVerdictQuery: (arg: unknown) => (seen.verdict.push(arg), okQuery({ verdict: { decision: "invalid", invalid_reasons: ["r"] } })),
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: (arg: unknown) => (seen.verdict.push(arg), okQuery({ verdict: { decision: "invalid", invalid_reasons: ["r"] } })),
	useGetModelMatrixQuery: (arg: unknown) => (seen.matrix.push(arg), { isLoading: true }),
	useGetClustersQuery: () => okQuery({ clusters: [] }),
	useGetAgentsQuery: () => okQuery({ agents: [] }),
	useGetExperimentQuery: () => okQuery({ exp_id: "e_x", arms: ["A", "B"], setups: [] }),
	useGetSuitesQuery: () => okQuery({ suites: [] }),
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({ useGetAwarenessQuery: () => okQuery({ per_arm: {} }) }));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/analysisApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/analysisApi")>()),
	useAuditTableMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({ useGetExperimentAdaptiveQuery: () => ({ data: { decision: "invalid" } }) }));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetBenchFaultsQuery: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => ({ data: { configured: false, connected: false } }),
	useListGithubReposQuery: () => ({}),
	useGetGithubRefsQuery: () => ({}),
}));

const { AnalysisTab } = await import("./analysisTab");
const { MatrixTab } = await import("./matrixTab");
const { ActionsTab } = await import("./actionsTab");

describe("tabs follow the grade the page shows", () => {
	it("Analysis asks the verdict of the original when the page shows it", () => {
		seen.verdict = [];
		render(<AnalysisTab expId="e_x" gradeSource="original" />);
		expect(seen.verdict).toContainEqual({ expId: "e_x", gradeSource: "original" });
		expect(seen.verdict).not.toContain("e_x");
	});

	it("Matrix asks the original matrix and verdict", () => {
		seen.verdict = [];
		seen.matrix = [];
		render(<MatrixTab expId="e_x" gradeSource="original" />);
		expect(seen.matrix[0]).toMatchObject({ expId: "e_x", grade_source: "original" });
		expect(seen.verdict).toContainEqual({ expId: "e_x", gradeSource: "original" });
	});

	it("Actions reads the broken parent's reason from the original", () => {
		seen.verdict = [];
		render(<ActionsTab expId="e_x" gradeSource="original" />);
		expect(seen.verdict).toContainEqual({ expId: "e_x", gradeSource: "original" });
		expect(seen.verdict).not.toContain("e_x");
	});

	it("without a grade source each tab keeps the server's default", () => {
		seen.verdict = [];
		render(<AnalysisTab expId="e_x" />);
		expect(seen.verdict).toEqual(["e_x"]);
	});
});