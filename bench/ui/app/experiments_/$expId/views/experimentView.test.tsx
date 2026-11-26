import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import ExperimentView from "./experimentView";

// QC2-28-04: while a new experiment route loads, RTK hands back the previous experiment's status as
// data. The screen must not render that status, or its Start action, under the new id.

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
vi.mock("@/components/shell/routeTabs", () => ({ RouteTabs: () => null }));
vi.mock("@/lib/store/apis/validityApi", () => ({ useGetExperimentValidityQuery: () => ({}), useMarkValiditySeenMutation: () => [() => undefined, {}] }));

const created = (id: string) => ({ exp_id: id, name: id, state: "created", slots: { total: 2, done: 0, running: 0, failed: 0 } });

describe("ExperimentView identity (QC2-28-04)", () => {
	it("shows the loading state, not the previous experiment's Start, while the new id loads", () => {
		api.status = okQuery(created("e_previous"));
		const html = render(<ExperimentView expId="e_next" />);
		expect(html).toContain('data-testid="experiment-loading"');
		expect(html).not.toContain("experiment-start");
	});
	it("renders the header and Start once the response matches the route", () => {
		api.status = okQuery(created("e_next"));
		const html = render(<ExperimentView expId="e_next" />);
		expect(html).toContain('data-testid="experiment-start"');
	});
});

describe("ExperimentView delete (X16)", () => {
	it("offers Delete to an admin, disabled with the reason while the comparison runs", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { tagOf } = await import("@/lib/testing/render");
		api.status = okQuery(created("e_done"));
		const idle = render(<ExperimentView expId="e_done" />, hostedSession("admin"));
		expect(tagOf(idle, "experiment-delete")).not.toContain('disabled=""');
		api.status = okQuery({ ...created("e_run"), state: "running" });
		const running = render(<ExperimentView expId="e_run" />, hostedSession("admin"));
		expect(tagOf(running, "experiment-delete")).toContain('disabled=""');
		expect(running).toContain("Stop the comparison or wait for it to finish before deleting it.");
	});
	it("hides Delete below admin", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		api.status = okQuery(created("e_done"));
		const html = render(<ExperimentView expId="e_done" />, hostedSession("member"));
		expect(html).not.toContain('data-testid="experiment-delete"');
		expect(html).toContain("experiment-delete-admin-only");
	});
});

describe("ExperimentView one benchmark of an evaluation (J7.tabs)", () => {
	const done = (extra: Record<string, unknown>) => ({ ...created("e_child"), state: "done", ...extra });
	it("says what a one-setup evaluation child is and links the evaluation", () => {
		api.status = okQuery(done({ arms: ["A"], evaluation_id: "ev_1" }));
		const html = render(<ExperimentView expId="e_child" />);
		expect(html).toContain('data-testid="experiment-evaluation-note"');
		expect(html).toContain(
			"This is one benchmark of an evaluation. Result, Model switch and Extra trials appear when two setups run on the same tasks.",
		);
		expect(html).toContain("Open the evaluation");
		expect(html).toContain('href="/evaluations/ev_1"');
	});
	it("has no note on a comparison of two setups or one outside an evaluation", () => {
		api.status = okQuery(done({ arms: ["A", "B"], evaluation_id: "ev_1" }));
		expect(render(<ExperimentView expId="e_child" />)).not.toContain("experiment-evaluation-note");
		api.status = okQuery(done({ arms: ["A"] }));
		expect(render(<ExperimentView expId="e_child" />)).not.toContain("experiment-evaluation-note");
	});
});
describe("ExperimentView header (fix wave 3)", () => {
	const at = (extra: Record<string, unknown>) => ({
		...created("e_h"),
		name: "zz-test-c10-matrix",
		suite: "coding-core@1.0.0",
		arms: ["A", "B"],
		...extra,
	});
	const verdict = (decision: string, extra: Record<string, unknown> = {}) => okQuery({ exp_id: "e_h", verdict: { decision }, ...extra });
	it("shows no decision while the comparison runs, only Result not final (N197)", () => {
		api.status = okQuery(at({ state: "running" }));
		api.verdict = verdict("invalid", { partial: true });
		const html = render(<ExperimentView expId="e_h" />);
		expect(html).not.toContain('data-testid="experiment-decision"');
		expect(html).not.toContain("Test was broken");
		expect(html).toContain('data-testid="experiment-partial"');
		expect(html).toContain("Result not final");
		api.status = okQuery(at({ state: "done" }));
		api.verdict = verdict("improved");
		const done = render(<ExperimentView expId="e_h" />);
		expect(done).toContain('data-testid="experiment-decision"');
		expect(done).not.toContain("Result not final");
		api.verdict = {};
	});
	it("links a follow-up to the comparison it extends (N246)", () => {
		api.status = okQuery(at({ state: "done", extends: "e_parent" }));
		api.parent = okQuery({ ...created("e_parent"), name: "zz-test-c10-matrix", state: "done" });
		const html = render(<ExperimentView expId="e_h" />);
		expect(html).toContain('data-testid="experiment-follow-up"');
		expect(html).toContain("Follow-up of");
		expect(html).toContain('href="/experiments/e_parent"');
	});
	it("labels a readiness check by its kind with a link to the ready tasks (N253)", () => {
		api.status = okQuery(at({ exp_id: "e_admit_acme_safety_5e8q", name: "admit-acme-safety", suite: "acme-safety@1.0.0", state: "done" }));
		api.verdict = verdict("inconclusive");
		const html = render(<ExperimentView expId="e_admit_acme_safety_5e8q" />);
		api.verdict = {};
		expect(html).toContain('data-testid="experiment-kind"');
		expect(html).toContain("Readiness check");
		expect(html).not.toContain('data-testid="experiment-decision"');
		expect(html).not.toContain("No clear winner yet");
		expect(html).toContain('href="/suites/acme-safety?tab=build"');
	});
	it("labels a debug run not scored, with its run outcome (N247)", () => {
		api.status = okQuery(
			at({
				exp_id: "e_fork_r_n697kiewjt_at_call_1_rqq1",
				name: "fork",
				debug: "fork",
				debug_from_run: "r_n697kiewjt",
				arms: ["A"],
				state: "done",
			}),
		);
		api.verdict = verdict("invalid");
		api.runs = okQuery({ runs: [{ run_id: "r_mp2np36f5x", outcome: "pass", sealed: true }], total: 1 });
		const html = render(<ExperimentView expId="e_fork_r_n697kiewjt_at_call_1_rqq1" />);
		api.verdict = {};
		api.runs = {};
		expect(html).toContain("Debug run (not scored)");
		expect(html).toContain("Solved");
		expect(html).not.toContain("Test was broken");
	});
	it("titles an evaluation benchmark after its evaluation, with an evaluation crumb (N203)", () => {
		api.status = okQuery(
			at({ name: "eval-20261122-143334-423797-hd-harbor-bfcl", suite: "hd-harbor-bfcl@1.0.0", evaluation_id: "ev_two", state: "done" }),
		);
		api.evaluation = okQuery({ id: "ev_two", spec: { name: "codex: two models" } });
		const html = render(<ExperimentView expId="e_h" />);
		api.evaluation = {};
		expect(html).toContain("codex: two models · BFCL function calling");
		expect(html).toContain("Evaluation: codex: two models");
		expect(html).toContain('href="/evaluations/ev_two"');
	});
});