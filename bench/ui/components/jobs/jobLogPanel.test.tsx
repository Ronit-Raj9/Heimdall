import "@/lib/i18n";

import type { JobLogState } from "@/components/platform/useJobLog";
import { FAILED_JOB, RUNNING_JOB, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { JobLogPanel, refetchOnLogEnd } from "./jobLogPanel";

const api = vi.hoisted(() => ({ job: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => api.job,
	useCancelJobMutation: () => mutation(),
}));
const log: JobLogState = {
	lines: [{ seq: 1, at: "2026-11-18T05:31:02Z", level: "info", line: "mining 300 commits" }],
	dropped: 0,
	ended: false,
	transport: "stream",
	error: undefined,
};
vi.mock("@/components/platform/useJobLog", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/platform/useJobLog")>()),
	useJobLog: () => log,
}));

beforeEach(() => {
	api.job = okQuery({ ...RUNNING_JOB, kind: "tasks.mine", mode: "live" });
});

describe("JobLogPanel", () => {
	it("streams the log of a running job with cancel", () => {
		const html = render(<JobLogPanel jobId={RUNNING_JOB.id} onClose={() => {}} />, hostedSession("member"));
		expect(html).toContain("Make tasks from past commits");
		expect(html).toContain("mining 300 commits");
		expect(html).toContain('data-testid="job-log-panel-cancel"');
		expect(html).toContain('data-testid="job-log-panel-close"');
	});

	it("shows the error of a failed job and no cancel", () => {
		api.job = okQuery({ ...FAILED_JOB, mode: "live" });
		const html = render(<JobLogPanel jobId={FAILED_JOB.id} />, hostedSession("member"));
		expect(html).toContain('data-testid="job-log-panel-error"');
		expect(html).not.toContain('data-testid="job-log-panel-cancel"');
	});

	it("links the experiment a bridge job ran on a runner", () => {
		api.job = okQuery({ ...FAILED_JOB, kind: "suite.admit", mode: "live", result: { exp_id: "e_abc123" } });
		const html = render(<JobLogPanel jobId={FAILED_JOB.id} />, hostedSession("member"));
		expect(html).toContain('data-testid="job-log-panel-experiment"');
		expect(html).toContain("e_abc123");
	});

	it("links a no-runner failure to Settings > Workers", () => {
		api.job = okQuery({ ...FAILED_JOB, mode: "live", error: "No worker online: this job starts containers" });
		const html = render(<JobLogPanel jobId={FAILED_JOB.id} />, hostedSession("member"));
		expect(html).toContain('data-testid="job-log-panel-add-runner"');
		expect(html).toContain("Settings &gt; Workers");
	});

	it("offers no runner link for other failures", () => {
		api.job = okQuery({ ...FAILED_JOB, mode: "live", error: "exit 1" });
		expect(render(<JobLogPanel jobId={FAILED_JOB.id} />, hostedSession("member"))).not.toContain("job-log-panel-add-runner");
	});
});

describe("refetchOnLogEnd (N63)", () => {
	it("asks for the job row once when the log ended while the row still says running", () => {
		expect(refetchOnLogEnd(true, RUNNING_JOB)).toBe(true);
		expect(refetchOnLogEnd(false, RUNNING_JOB)).toBe(false);
		expect(refetchOnLogEnd(true, FAILED_JOB)).toBe(false);
		expect(refetchOnLogEnd(true, undefined)).toBe(false);
	});
});
describe("JobLogPanel cost estimates (N275)", () => {
	it("titles an estimate job as a cost estimate", () => {
		api.job = okQuery({ ...RUNNING_JOB, kind: "policy.generate", params: { estimate: true }, mode: "live" });
		expect(render(<JobLogPanel jobId={RUNNING_JOB.id} />, hostedSession("member"))).toContain("(Cost estimate)");
		api.job = okQuery({ ...RUNNING_JOB, kind: "policy.generate", params: { yes: true }, mode: "live" });
		expect(render(<JobLogPanel jobId={RUNNING_JOB.id} />, hostedSession("member"))).not.toContain("(Cost estimate)");
	});
});