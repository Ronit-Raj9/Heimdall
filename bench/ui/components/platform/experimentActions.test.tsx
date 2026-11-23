import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { LOCAL_SESSION, REGRADE_JOB, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExperimentRunActions, RegradeAction, regradeIdOf } from "./experimentActions";
import { EMPTY_REGRADE_FORM, RegradeDialog, regradeFormProblem, regradeRequest, suggestRegradeId } from "./regradeDialog";

const api = vi.hoisted(() => ({
	regrade: {} as Record<string, unknown>,
	cancel: {} as Record<string, unknown>,
	resume: {} as Record<string, unknown>,
	start: {} as Record<string, unknown>,
	health: undefined as unknown,
	controls: undefined as unknown,
	finished: undefined as unknown,
	dispatched: [] as unknown[],
}));
vi.mock("@/components/jobs/jobLogPanel", () => ({
	JobLogPanel: ({ jobId, onFinished }: { jobId: string; onFinished?: (job: unknown) => void }) => {
		if (api.finished) onFinished?.(api.finished);
		return <div data-testid="job-log-panel">{jobId}</div>;
	},
}));
vi.mock("@/lib/store", () => ({ useAppDispatch: () => (action: unknown) => api.dispatched.push(action) }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelExperimentMutation: () => mutation(api.cancel),
	useResumeExperimentMutation: () => mutation(api.resume),
}));
vi.mock("@/lib/store/apis/runControlApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/runControlApi")>()),
	useGetRunControlsQuery: () => ({ data: api.controls }),
	usePauseExperimentMutation: () => mutation({}),
	useUnpauseExperimentMutation: () => mutation({}),
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useRegradeMutation: () => mutation(api.regrade),
	useStartExperimentMutation: () => mutation(api.start),
	useGetHealthQuery: () => ({ data: api.health }),
}));

const status = (state: ExperimentStatus["state"]) =>
	({ exp_id: "e_nightly", state, slots: { total: 4, done: 1, running: 1, failed: 0 } }) as unknown as ExperimentStatus;

beforeEach(() => {
	api.regrade = {};
	api.cancel = {};
	api.resume = {};
	api.health = undefined;
	api.controls = undefined;
});

describe("ExperimentRunActions", () => {
	it("puts Pause beside Cancel while running, and Resume with a draining chip once paused", () => {
		api.controls = { exp_id: "e_nightly", paused: false, in_flight: 0, state: { mode: "running", seq: 0 } };
		const html = render(<ExperimentRunActions status={status("running")} />, hostedSession("member"));
		expect(html).toContain('data-testid="experiment-pause"');
		expect(html).toContain('data-testid="experiment-cancel"');
		api.controls = { exp_id: "e_nightly", paused: true, in_flight: 2, state: { mode: "paused", paused_by: "ana", seq: 1 } };
		const paused = render(<ExperimentRunActions status={status("running")} />, hostedSession("member"));
		expect(paused).toContain('data-testid="experiment-unpause"');
		expect(paused).toContain("Pausing: 2 runs still going");
		expect(paused).not.toContain('data-testid="experiment-pause"');
	});
	it("offers Start again for a created experiment whose start failed (6-A-07)", () => {
		const html = render(<ExperimentRunActions status={status("created")} />, hostedSession("member"));
		expect(html).toContain('data-testid="experiment-start"');
		expect(html).not.toContain("experiment-resume");
	});
	it("hides Start on a server without a launcher, keeps Resume (QC2-27-03)", () => {
		api.health = { mode: "live", launch: false };
		expect(render(<ExperimentRunActions status={status("created")} />, LOCAL_SESSION)).toBe("");
		expect(render(<ExperimentRunActions status={status("failed")} />, LOCAL_SESSION)).toContain("experiment-resume");
	});
	it("offers cancel while running and resume after a cancel or failure", () => {
		expect(render(<ExperimentRunActions status={status("running")} />, hostedSession("member"))).toContain(
			'data-testid="experiment-cancel"',
		);
		for (const state of ["cancelled", "failed"] as const) {
			const html = render(<ExperimentRunActions status={status(state)} />, LOCAL_SESSION);
			expect(html).toContain('data-testid="experiment-resume"');
			expect(html).not.toContain("experiment-cancel");
		}
		expect(render(<ExperimentRunActions status={status("done")} />, hostedSession())).toBe("");
	});
	it("hides from demo visitors and explains to viewers", () => {
		expect(render(<ExperimentRunActions status={status("running")} />, demoSession())).toBe("");
		expect(render(<ExperimentRunActions status={status("running")} />, hostedSession("viewer"))).toContain("Needs member or above");
	});
	it("shows a hosted 403 as the role refusal", () => {
		api.cancel = { error: { status: 403, data: { error: { message: "jobs need the member role" } } } };
		const html = render(<ExperimentRunActions status={status("running")} />, hostedSession("member"));
		expect(html).toContain("Your role cannot do this");
	});
});

describe("RegradeAction", () => {
	it("starts a regrade job and links to it once queued", () => {
		expect(render(<RegradeAction expId="e_nightly" />, hostedSession("member"))).toContain('data-testid="regrade-button"');
		api.regrade = { data: { ...REGRADE_JOB, mode: "live" }, originalArgs: { exp_id: "e_nightly" } };
		const html = render(<RegradeAction expId="e_nightly" />, hostedSession("member"));
		expect(html).toContain("Job job_rg started");
		expect(html).toContain('href="/jobs/job_rg"');
	});
	it("shows nothing to demo visitors or viewers, and the server's reason on failure", () => {
		expect(render(<RegradeAction expId="e_nightly" />, demoSession())).toBe("");
		expect(render(<RegradeAction expId="e_nightly" />, hostedSession("viewer"))).toBe("");
		api.regrade = { error: { status: 503, data: { error: { message: "regrade (job runner) is not available" } } } };
		expect(render(<RegradeAction expId="e_nightly" />)).toContain("regrade (job runner) is not available");
	});
	it("opens the regate options dialog", () => {
		const html = render(<RegradeAction expId="e_nightly" defaultOpen />, hostedSession("member"));
		expect(html).toContain('data-testid="regrade-dialog"');
		expect(html).toContain('data-testid="regrade-lock-current"');
		expect(html).toContain('data-testid="regrade-note"');
		expect(html).toContain('data-testid="regrade-id"');
		expect(html).toContain("Re-score e_nightly");
	});
	it("shows the queued job's log and the verdict at the new grade source", () => {
		api.regrade = { data: { ...REGRADE_JOB, mode: "live" }, originalArgs: { exp_id: "e_nightly" } };
		const html = render(<RegradeAction expId="e_nightly" />, hostedSession("member"));
		expect(html).toContain('data-testid="job-log-panel"');
		expect(html).toContain('data-testid="regrade-open-verdict"');
		expect(html).toContain("rg_42");
		expect(regradeIdOf({ ...REGRADE_JOB, params: { exp_id: "e" } })).toBe("rg_42");
	});
	it("re-reads the comparison's status once the re-score succeeded, so the page shows it", () => {
		api.regrade = { data: { ...REGRADE_JOB, status: "queued" }, originalArgs: { exp_id: "e_nightly" } };
		api.dispatched = [];
		api.finished = { ...REGRADE_JOB, status: "failed" };
		render(<RegradeAction expId="e_nightly" />, hostedSession("member"));
		expect(api.dispatched).toEqual([]);
		api.finished = { ...REGRADE_JOB, status: "succeeded" };
		render(<RegradeAction expId="e_nightly" />, hostedSession("member"));
		expect(JSON.stringify(api.dispatched)).toContain('"type":"BenchExperiment","id":"e_nightly"');
		api.finished = undefined;
	});
});

describe("regrade form", () => {
	it("sends only the options set", () => {
		expect(regradeRequest(EMPTY_REGRADE_FORM)).toEqual({});
		expect(regradeRequest({ lockChoice: "frozen", suiteLock: " experiments/x/suite.lock ", note: " gates v2 ", id: "rg_v2" })).toEqual({
			id: "rg_v2",
			note: "gates v2",
			suite_lock: "experiments/x/suite.lock",
		});
		expect(regradeRequest({ ...EMPTY_REGRADE_FORM, lockChoice: "current", suiteLock: "ignored" })).toEqual({});
	});
	it("names the first problem", () => {
		expect(regradeFormProblem(EMPTY_REGRADE_FORM)).toBeNull();
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, id: "original" })).toBe("reservedId");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, id: "-bad" })).toBe("badId");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, lockChoice: "frozen" })).toBe("needLock");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, lockChoice: "frozen", suiteLock: "../x.lock" })).toBe("badLock");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, note: "x".repeat(2001) })).toBe("longNote");
	});
});

describe("re-score name", () => {
	it("refuses a name the store refuses and suggests the converted one", () => {
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, id: "zz-test-rescore" })).toBe("badId");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, id: "Rg.V2" })).toBe("badId");
		expect(regradeFormProblem({ ...EMPTY_REGRADE_FORM, id: "zz_test_rescore" })).toBeNull();
		expect(suggestRegradeId("zz-test-rescore")).toBe("zz_test_rescore");
		expect(suggestRegradeId(" Rg.V2 ")).toBe("rg_v2");
		expect(suggestRegradeId("bad name!")).toBeNull();
	});
	it("shows the rule under the field and offers the fix", () => {
		const html = render(
			<RegradeDialog expId="e_1" initial={{ ...EMPTY_REGRADE_FORM, id: "zz-test-rescore" }} onSubmit={() => {}} onClose={() => {}} />,
		);
		expect(html).toContain("re-score name zz-test-rescore: use lower-case letters, digits and _ only, for example zz_test_rescore");
		expect(html).toContain('data-testid="regrade-id-fix"');
		expect(html).toContain("Use zz_test_rescore");
	});
});