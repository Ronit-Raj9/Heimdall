import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { LOCAL_SESSION, REGRADE_JOB, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExperimentRunActions, RegradeAction } from "./experimentActions";

const api = vi.hoisted(() => ({
	regrade: {} as Record<string, unknown>,
	cancel: {} as Record<string, unknown>,
	resume: {} as Record<string, unknown>,
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelExperimentMutation: () => mutation(api.cancel),
	useResumeExperimentMutation: () => mutation(api.resume),
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useRegradeMutation: () => mutation(api.regrade),
}));

const status = (state: ExperimentStatus["state"]) =>
	({ exp_id: "e_nightly", state, slots: { total: 4, done: 1, running: 1, failed: 0 } }) as unknown as ExperimentStatus;

beforeEach(() => {
	api.regrade = {};
	api.cancel = {};
	api.resume = {};
});

describe("ExperimentRunActions", () => {
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
});