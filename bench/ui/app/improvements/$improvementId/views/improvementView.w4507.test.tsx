import "@/lib/i18n";

import { IMPROVEMENT_FIXTURE, stateFixture } from "@/components/improvements/improve.fixtures";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import ImprovementView from "./improvementView";

const api = vi.hoisted(() => ({ one: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useGetImprovementQuery: () => api.one,
	useApproveImprovementRoundMutation: () => mutation(),
	useStopImprovementMutation: () => mutation(),
	useResumeImprovementMutation: () => mutation(),
	useContinueImprovementMutation: () => mutation(),
	useContinueEstimateMutation: () => mutation(),
	useGetVersionExplainQuery: () => okQuery(undefined),
}));

describe("ImprovementView Resume", () => {
	it("shows Resume to an admin only when the run can resume", () => {
		api.one = okQuery({ ...IMPROVEMENT_FIXTURE, status: "interrupted", can_resume: true });
		expect(render(<ImprovementView id="r1" />, hostedSession("admin"))).toContain('data-testid="improvement-resume"');
		expect(render(<ImprovementView id="r1" />, hostedSession("member"))).not.toContain('data-testid="improvement-resume"');
		api.one = okQuery({ ...IMPROVEMENT_FIXTURE, status: "done", can_resume: false });
		expect(render(<ImprovementView id="r1" />, hostedSession("admin"))).not.toContain('data-testid="improvement-resume"');
	});

	it("shows the status line while running", () => {
		api.one = okQuery({
			...stateFixture("supported"),
			status: "running",
			running: true,
			verdict_detail: undefined,
			rounds: 5,
			rounds_done: 1,
			round_views: [{ ...IMPROVEMENT_FIXTURE.round_views[0], n: 2, stage: "screen", decision: "" }],
			spent_usd: 3.5,
			limit_usd: 60,
		});
		expect(render(<ImprovementView id="r1" />, hostedSession("admin"))).toContain(
			"Round 2 of 5: trying ideas on practice tasks. Spent $3.50 of $60.00.",
		);
	});
});
// Round 5 lane L7: the study create and chain check hooks, outside a store.
vi.mock("@/lib/store/apis/improveStudyApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveStudyApi")>()),
	useCreateImprovementStudyMutation: () => [() => ({ unwrap: async () => ({ id: "ims_x" }) }), { isLoading: false }],
	useLazyCheckImprovementLineageQuery: () => [() => undefined, { isFetching: false, isError: false }],
}));
