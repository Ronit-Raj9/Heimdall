import "@/lib/i18n";

import type { AgentSummary, Job } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ChecksTab } from "./checksTab";

const state = vi.hoisted(() => ({
	agents: [] as Partial<AgentSummary>[],
	jobs: [] as Partial<Job>[],
	hosted: true,
}));

vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: state.agents }, isLoading: false }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { suites: [{ id: "bfcl" }, { id: "coding-core" }] }, isLoading: false }),
	useGetSuiteQuery: () => ({ data: { tasks: [{ id: "t1" }] }, isLoading: false }),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useListJobsQuery: () => ({ data: { jobs: state.jobs } }) }));
vi.mock("@/lib/store/apis/jobCountApi", () => ({ useCountRunningJobsQuery: () => ({ data: { count: 0, more: false } }) }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/components/platform/useJobLog", () => ({ useJobLog: () => ({ lines: [] }) }));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ authMode: state.hosted ? "hosted" : "local", role: "member" }),
}));
vi.mock("@tanstack/react-router", () => ({
	useParams: () => ({}),
	Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
		<a href={params?.jobId ? to.replace("$jobId", params.jobId) : to} {...rest}>
			{children}
		</a>
	),
}));

const loop: Partial<AgentSummary> = {
	id: "zz-test-loop",
	path: "agents/zz-test-loop.yaml",
	image: "bench/agent-zz-test-loop:1",
	dockerfile: "agents/images/zz-test-loop/Dockerfile",
	per_suite_build: true,
};

describe("readiness checklist (fix wave 3, N180, N181)", () => {
	it("a failed step shows the job error and a link to its log", () => {
		state.agents = [loop];
		state.jobs = [
			{
				id: "job_7",
				kind: "agent.check",
				status: "failed",
				params: { agent: "agents/zz-test-loop.yaml" },
				error:
					"agent check: ERROR: The test run could not start: the agent container bench/agent-zz-test-loop:1-coding-core does not exist on the worker yet.",
			},
		];
		const html = renderToStaticMarkup(<ChecksTab agentId="zz-test-loop" />);
		expect(html).toContain('data-testid="agent-check-section-error"');
		expect(html).toContain("does not exist on the worker yet.");
		expect(html).toContain('href="/jobs/job_7"');
	});

	it("a per-benchmark agent picks the benchmark from a list and Prepare waits for it", () => {
		state.agents = [loop];
		state.jobs = [];
		const html = renderToStaticMarkup(<ChecksTab agentId="zz-test-loop" />);
		expect(html).toContain('data-testid="agent-build-suite-select"');
		expect(html).toContain('<option value="bfcl">bfcl</option>');
		expect(html).toMatch(/data-testid="job-run-agent\.build"[^>]*disabled|disabled[^>]*data-testid="job-run-agent\.build"/);
		expect(html).toContain("Pick a benchmark first");
		expect(html).not.toContain("Name the benchmark this container is for.");
		expect(html).toContain("Runs on a worker, never the server.");
	});
});