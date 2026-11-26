import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import ExperimentView from "./experimentView";

// Wave 4, p05-5: the Runs tab of a running comparison passes running to RunList, so an empty list
// says the runs are on their way instead of offering a new comparison.

const api = vi.hoisted(() => ({
	status: {} as unknown,
	verdict: {} as unknown,
	parent: {} as unknown,
	evaluation: {} as unknown,
	runs: {} as unknown,
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/lib/store", () => ({ useAppDispatch: () => () => undefined }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetExperimentQuery: (id: unknown) => (id === "e_parent" ? api.parent : api.status),
	useGetVerdictQuery: () => api.verdict,
	useListRunsQuery: () => api.runs,
	useGetGridQuery: () => ({}),
	useGetHealthQuery: () => ({}),
	useStartExperimentMutation: () => [() => undefined, {}],
	useRegradeMutation: () => [() => undefined, {}],
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelExperimentMutation: () => [() => undefined, {}],
	useResumeExperimentMutation: () => [() => undefined, {}],
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetEvaluationQuery: () => api.evaluation,
}));
vi.mock("@/lib/store/apis/canaryApi", () => ({ useGetCanariesQuery: () => ({}) }));
vi.mock("@/lib/store/apis/dataOpsApi", () => ({ useDeleteExperimentMutation: () => [() => undefined, {}] }));
// Run controls (X21) beside Cancel.
vi.mock("@/lib/store/apis/runControlApi", async (orig) => ({
	...(await orig<object>()),
	useGetRunControlsQuery: () => ({}),
	usePauseExperimentMutation: () => [() => undefined, { isLoading: false }],
	useUnpauseExperimentMutation: () => [() => undefined, { isLoading: false }],
}));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({ useGetExperimentAdaptiveQuery: () => ({}) }));
vi.mock("@/lib/store/apis/supportApi", () => ({ useGetExperimentSupportQuery: () => ({}) }));
vi.mock("@/lib/store/apis/researchApi", () => ({ useGetExperimentResearchQuery: () => ({}) }));
vi.mock("@/lib/store/apis/routingApi", () => ({ useGetExperimentRoutingQuery: () => ({}) }));
vi.mock("./liveProgress", () => ({ LiveProgress: () => null, default: () => null }));
vi.mock("@/components/shell/routeTabs", () => ({ RouteTabs: ({ children }: { children: (active: string) => unknown }) => children("runs") }));
vi.mock("@/components/runs/runList", () => ({
	RunList: ({ running }: { running?: boolean }) => <span data-testid="run-list-probe" data-running={String(Boolean(running))} />,
}));

const exp = (state: string) => ({ exp_id: "e_run", name: "e_run", state, slots: { total: 2, done: 0, running: 1, failed: 0 } });

describe("ExperimentView Runs tab (p05-5)", () => {
	it("tells RunList the comparison is still running", () => {
		api.status = okQuery(exp("running"));
		const html = render(<ExperimentView expId="e_run" />);
		expect(html).toContain('data-testid="run-list-probe" data-running="true"');
	});
	it("does not say running once the comparison has finished", () => {
		api.status = okQuery(exp("done"));
		const html = render(<ExperimentView expId="e_run" />);
		expect(html).toContain('data-testid="run-list-probe" data-running="false"');
	});
});
