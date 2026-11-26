import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { FromIssues } from "./fromIssues";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/issuebenchApi", () => ({
	useGetIssueReadinessQuery: () => okQuery({ hosted: true, worker_online: true, github: true, gateway: true, default_model: true }),
	useGetIssueRunQuery: () => okQuery(undefined),
	useScanIssuesMutation: () => mutation(),
	useStartIssueRunMutation: () => mutation(),
	useReviewIssueMutation: () => mutation(),
	useRetryIssueMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => okQuery({ connected: true }),
	useListGithubReposQuery: () => okQuery({ repos: [] }),
}));
vi.mock("@/components/jobs/jobLogPanel", () => ({ JobLogPanel: () => null }));

describe("FromIssues languages note", () => {
	it("says which languages the builder handles, under the repository field", () => {
		const out = render(<FromIssues writable />, hostedSession("member"));
		expect(tagOf(out, "issuebench-languages")).toBeTruthy();
		expect(out).toContain(
			"Works with Python (pytest), JavaScript and TypeScript (jest, vitest) and Go. Other languages are skipped with a reason.",
		);
		expect(out.indexOf('data-testid="issuebench-languages"')).toBeGreaterThan(out.indexOf('data-testid="issuebench-scan"'));
	});
});