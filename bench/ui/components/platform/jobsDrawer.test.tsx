import "@/lib/i18n";

import type { JobList } from "@/lib/types/bench";
import { FAILED_JOB, LOCAL_SESSION, REGRADE_JOB, RUNNING_JOB, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, okQuery, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { JobsDrawer, JobsIndicator } from "./jobsDrawer";

const api = vi.hoisted(() => ({ jobs: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useListJobsQuery: () => api.jobs,
}));

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/jobs.json"), "utf8")) as JobList;

describe("JobsIndicator", () => {
	beforeEach(() => {
		api.jobs = okQuery({ mode: "live", jobs: [RUNNING_JOB, FAILED_JOB, REGRADE_JOB], kinds: [] });
	});

	it("counts running jobs in the header, in hosted and local mode", () => {
		for (const session of [hostedSession("viewer"), LOCAL_SESSION]) {
			const html = render(<JobsIndicator />, session);
			expect(html).toContain('data-testid="jobs-indicator"');
			expect(html).toContain('data-running="1"');
			expect(html).toContain('data-testid="jobs-indicator-count"');
			expect(html).toContain("1 job running");
		}
	});

	it("shows no count when nothing runs", () => {
		api.jobs = okQuery({ mode: "live", jobs: [FAILED_JOB], kinds: [] });
		const html = render(<JobsIndicator />, hostedSession());
		expect(html).toContain("No jobs running");
		expect(html).not.toContain("jobs-indicator-count");
	});

	it("hides on a server without the jobs API, on 401 and 403, and for demo visitors", () => {
		for (const status of [404, 401, 403]) {
			api.jobs = errorQuery(status);
			expect(render(<JobsIndicator />, hostedSession())).toBe("");
		}
		api.jobs = okQuery({ mode: "live", jobs: [RUNNING_JOB], kinds: [] });
		expect(render(<JobsIndicator />, demoSession())).toBe("");
	});
});

describe("JobsDrawer", () => {
	it("lists running jobs first with progress, then recent ones with their errors, each linking to its page", () => {
		const html = render(
			<JobsDrawer jobs={fixture.jobs} isLoading={false} onRetry={() => undefined} onClose={() => undefined} />,
			hostedSession(),
		);
		expect(html).toContain('role="dialog"');
		expect(html).toContain('aria-modal="true"');
		const running = html.slice(html.indexOf('data-testid="jobs-drawer-running"'), html.indexOf('data-testid="jobs-drawer-recent"'));
		expect(running).toContain('href="/jobs/job_synth0001"');
		expect(running).toContain("Experiment run");
		expect(running).toContain('data-testid="job-progress-job_synth0001"');
		const recent = html.slice(html.indexOf('data-testid="jobs-drawer-recent"'));
		expect(recent).toContain("Suite lint");
		expect(recent).toContain('data-testid="job-status-failed"');
		expect(recent).toContain("refused the connection");
		expect(html).toContain("Running and recent work in Acme Agents.");
	});

	it("says when nothing runs and when nothing finished", () => {
		const html = render(<JobsDrawer jobs={[]} isLoading={false} onRetry={() => undefined} onClose={() => undefined} />);
		expect(html).toContain("Nothing is running.");
		expect(html).toContain("No finished jobs yet.");
		expect(html).toContain("Running and recent work on this server.");
	});

	it("shows loading and errors", () => {
		expect(render(<JobsDrawer jobs={[]} isLoading onRetry={() => undefined} onClose={() => undefined} />)).toContain("jobs-drawer-loading");
		const html = render(
			<JobsDrawer
				jobs={[]}
				isLoading={false}
				error={errorQuery(500, "job table unreadable").error}
				onRetry={() => undefined}
				onClose={() => undefined}
			/>,
		);
		expect(html).toContain('data-testid="jobs-drawer-error"');
		expect(html).toContain("job table unreadable");
	});

	it("shows a loading indicator query without crashing", () => {
		api.jobs = loadingQuery();
		expect(render(<JobsIndicator />, hostedSession())).toContain("jobs-indicator");
	});
});