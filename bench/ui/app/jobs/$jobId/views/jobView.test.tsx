import "@/lib/i18n";

import { appendLines, type JobLogState } from "@/components/platform/useJobLog";
import type { JobLogLine } from "@/lib/types/bench";
import { FAILED_JOB, LOCAL_SESSION, REGRADE_JOB, RUNNING_JOB, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import JobView, { JobDetail, JobLogPanel, regradeIdOf } from "./jobView";

const api = vi.hoisted(() => ({ job: {} as unknown, log: {} as unknown, cancel: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => api.job,
	useCancelJobMutation: () => mutation(api.cancel),
}));
vi.mock("@/components/platform/useJobLog", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/platform/useJobLog")>()),
	useJobLog: () => api.log,
}));

const line = (seq: number, text: string, level = "info"): JobLogLine => ({ seq, at: "2026-11-18T05:31:02Z", level, line: text });
const liveLog: JobLogState = {
	lines: [line(1, "lease s_t01_A"), line(2, "gateway 429", "warn")],
	dropped: 0,
	ended: false,
	transport: "stream",
	error: undefined,
};

beforeEach(() => {
	api.job = okQuery({ ...RUNNING_JOB, mode: "live" });
	api.log = liveLog;
	api.cancel = {};
});

describe("JobView", () => {
	it("shows a running job: kind, status, progress, live log and cancel", () => {
		const html = render(<JobView jobId="job_run" />, hostedSession("member"));
		expect(tagOf(html, "job-screen")).toContain('data-status="running"');
		expect(html).toContain("Experiment run");
		expect(html).toContain('data-testid="job-status-running"');
		expect(html).toContain("30 of 48");
		expect(html).toContain('data-testid="job-log-live"');
		expect(html).toContain('data-testid="job-log-line-2"');
		expect(html).toContain("gateway 429");
		expect(html).toContain('data-testid="job-cancel"');
		expect(html).toContain('href="/verdict?exp=e_nightly"');
	});

	it("prints log sequence numbers in an opaque readable ink on the terminal (6-B-08)", () => {
		const html = render(<JobView jobId="job_run" />, hostedSession("member"));
		const seq = tagOf(html, "job-log-seq-2") ?? "";
		expect(seq).toContain("text-zinc-500");
		expect(seq).not.toMatch(/text-zinc-500\//);
	});
	it("offers no cancel to viewers or demo visitors, and cancel in local mode", () => {
		expect(render(<JobView jobId="job_run" />, hostedSession("viewer"))).not.toContain('data-testid="job-cancel"');
		expect(render(<JobView jobId="job_run" />, demoSession())).not.toContain('data-testid="job-cancel"');
		expect(render(<JobView jobId="job_run" />, LOCAL_SESSION)).toContain('data-testid="job-cancel"');
	});

	it("shows a failed job's error and no cancel", () => {
		api.job = okQuery({ ...FAILED_JOB, mode: "live" });
		api.log = { ...liveLog, ended: true };
		const html = render(<JobView jobId="job_fail" />, hostedSession());
		expect(html).toContain('data-testid="job-error-block"');
		expect(html).toContain("gateway refused the connection");
		expect(html).not.toContain('data-testid="job-cancel"');
		expect(html).toContain('data-testid="job-log-done"');
	});

	it("opens the verdict on the regrade's grades once it succeeded", () => {
		api.job = okQuery({ ...REGRADE_JOB, mode: "live" });
		const html = render(<JobView jobId="job_rg" />, hostedSession());
		expect(html).toContain('href="/verdict?exp=e_nightly&amp;grade=rg_42"');
		expect(html).toContain('data-testid="job-result"');
		expect(regradeIdOf(REGRADE_JOB)).toBe("rg_42");
		expect(regradeIdOf(RUNNING_JOB)).toBeNull();
	});

	it("shows a cancel request until the job stops", () => {
		api.job = okQuery({ ...RUNNING_JOB, cancel_requested: true, mode: "live" });
		const html = render(<JobView jobId="job_run" />, hostedSession());
		expect(html).toContain('data-testid="job-cancel-requested"');
		expect(html).not.toContain('data-testid="job-cancel"');
	});

	it("says a job is not in this organization on 404, the role on 403, and loads", () => {
		api.job = errorQuery(404, "job not found");
		expect(render(<JobView jobId="job_x" />, hostedSession())).toContain('data-testid="job-not-found"');
		api.job = errorQuery(403, "jobs need the viewer role");
		expect(render(<JobView jobId="job_x" />, hostedSession())).toContain("Your role cannot do this");
		api.job = loadingQuery();
		expect(render(<JobView jobId="job_x" />, hostedSession())).toContain("job-loading");
	});
});

describe("job log", () => {
	it("appends only new lines in order and keeps the tail", () => {
		const a = appendLines([], [line(2, "b"), line(1, "a")]);
		expect(a.map((l) => l.seq)).toEqual([1, 2]);
		expect(appendLines(a, [line(2, "b"), line(3, "c")]).map((l) => l.seq)).toEqual([1, 2, 3]);
		expect(appendLines(a, [line(1, "a")])).toBe(a);
	});
	it("says when lines were dropped and when there are none yet", () => {
		const html = render(<JobLogPanel log={{ ...liveLog, lines: [], dropped: 120 }} active />);
		expect(html).toContain("120 earlier lines were dropped");
		expect(html).toContain('data-testid="job-log-empty"');
		expect(html).toContain('role="log"');
	});
	it("renders a detail for a job given directly", () => {
		expect(render(<JobDetail job={RUNNING_JOB} log={liveLog} />, hostedSession())).toContain("job-facts");
	});
});