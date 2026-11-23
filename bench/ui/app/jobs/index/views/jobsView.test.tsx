import "@/lib/i18n";

import { FAILED_JOB, LOCAL_SESSION, RUNNING_JOB, hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import JobsView from "./jobsView";

const api = vi.hoisted(() => ({ list: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/jobsApi", () => ({ useListJobsFilteredQuery: () => api.list }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelJobMutation: () => mutation(),
}));

const CLI_JOB = { ...FAILED_JOB, id: "job_export", kind: "export.run", target: "exp_1" };

beforeEach(() => {
	api.list = okQuery({ mode: "live", jobs: [RUNNING_JOB, FAILED_JOB, CLI_JOB], kinds: ["experiment.run", "export.run", "tasks.mine"] });
});

describe("JobsView", () => {
	it("lists jobs with filters, cancel on running jobs and retry on ended ones", () => {
		const html = render(<JobsView />, hostedSession("member"));
		expect(html).toContain('data-testid="jobs-filter-kind"');
		expect(html).toContain('data-testid="jobs-filter-status"');
		expect(html).toContain(`data-testid="jobs-row-${RUNNING_JOB.id}"`);
		expect(html).toContain(`data-testid="jobs-cancel-${RUNNING_JOB.id}"`);
		expect(html).toContain(`data-testid="jobs-retry-${FAILED_JOB.id}"`);
		expect(html).not.toContain(`data-testid="jobs-retry-${RUNNING_JOB.id}"`);
		expect(html).toContain("Download results of exp_1");
		expect(html).toContain("Make tasks from past commits");
	});

	it("gives viewers no cancel or retry", () => {
		const html = render(<JobsView />, hostedSession("viewer"));
		expect(html).toContain(`data-testid="jobs-row-${RUNNING_JOB.id}"`);
		expect(html).not.toContain("jobs-cancel-");
		expect(html).not.toContain("jobs-retry-");
	});

	it("shows an empty state with a next step, a loading state and an error", () => {
		api.list = okQuery({ mode: "live", jobs: [], kinds: [] });
		expect(render(<JobsView />, LOCAL_SESSION)).toContain('data-testid="jobs-empty"');
		api.list = loadingQuery();
		expect(render(<JobsView />, LOCAL_SESSION)).toContain('data-testid="jobs-loading"');
		api.list = errorQuery(500);
		expect(render(<JobsView />, LOCAL_SESSION)).toContain('data-testid="jobs-error"');
	});
});

describe("JobsView cost estimates (N275)", () => {
	it("marks an estimate job in the list", () => {
		const estimate = { ...RUNNING_JOB, id: "job_est", kind: "policy.generate", target: "desk", params: { estimate: true } };
		api.list = okQuery({ mode: "live", jobs: [estimate, FAILED_JOB], kinds: ["policy.generate"] });
		const html = render(<JobsView />, hostedSession("member"));
		expect(html).toContain("(Cost estimate) of desk");
		expect(html.match(/\(Cost estimate\)/g)).toHaveLength(1);
	});
});
