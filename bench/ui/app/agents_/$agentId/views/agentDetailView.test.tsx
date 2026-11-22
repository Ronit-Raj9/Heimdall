import "@/lib/i18n";

import { LOCAL_SESSION, type SessionInfo } from "@/lib/session/sessionContext";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import AgentDetailView from "./agentDetailView";

const state = vi.hoisted(() => ({ configMissing: false }));

vi.mock("@tanstack/react-router", async () => {
	const { routerMock } = await import("@/lib/testing/routerMock");
	return { ...routerMock, useParams: () => ({ agentId: "zz-test-loop" }) };
});
vi.mock("./agentOps.shared", () => ({
	useAgentSummary: () => ({ agent: { id: "zz-test-loop", adapter: "command", path: "agents/zz-test-loop.yaml" }, isLoading: false }),
}));
vi.mock("../../../agents/views/agentJobs", () => ({ useAgentJobs: () => [] }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useGetAgentCompatibilityQuery: () => ({ data: { capabilities: [] } }),
	useGetAgentVersionsQuery: () => ({ data: { versions: [] } }),
}));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	useGetAgentConfigQuery: () => (state.configMissing ? errorQuery(404, "not found") : okQuery({ id: "zz-test-loop" })),
}));
vi.mock("@/lib/store/apis/agentDeleteApi", () => ({ useDeleteAgentMutation: () => mutation() }));
vi.mock("./checksTab", () => ({ default: () => <p data-testid="stub-checks" /> }));
vi.mock("@/components/modelswitch/compareModelsForm", () => ({ CompareModelsForm: () => <p data-testid="stub-compare-models" /> }));
vi.mock("./agentRunsTab", () => ({ default: () => <p data-testid="stub-runs" /> }));
vi.mock("./configTab", () => ({ default: () => <p data-testid="stub-setup" /> }));
vi.mock("./pullRequestsTab", () => ({ default: () => <p data-testid="stub-pull-requests" /> }));
vi.mock("./reportCardTab", () => ({ default: () => <p data-testid="stub-report-card" /> }));
vi.mock("./toolsTab", () => ({ default: () => <p data-testid="stub-tools" /> }));
vi.mock("./versionsTab", () => ({ default: () => <p data-testid="stub-versions" /> }));
vi.mock("@/components/sharedpool/imageRequests", () => ({ AgentImageApproval: () => null }));
vi.mock("../../new/views/connectForm", () => ({ SavedEndpointTest: () => null }));

function page(search: string, session: SessionInfo = LOCAL_SESSION) {
	return render(
		<NuqsTestingAdapter searchParams={search}>
			<AgentDetailView />
		</NuqsTestingAdapter>,
		session,
	);
}

describe("agent page tabs opened from the menu", () => {
	it("?tab=checks opens the readiness check", () => {
		const html = page("?tab=checks");
		expect(html).toContain('data-testid="stub-checks"');
		expect(html).not.toContain('data-testid="stub-report-card"');
		expect(html).toContain('data-testid="tab-checks"');
	});

	it("?tab=compare-models opens the model comparison form", () => {
		const html = page("?tab=compare-models");
		expect(html).toContain('data-testid="stub-compare-models"');
		expect(html).not.toContain('data-testid="stub-report-card"');
	});

	it("the plain page shows the report card and no extra tab", () => {
		const html = page("");
		expect(html).toContain('data-testid="stub-report-card"');
		expect(html).not.toContain('data-testid="tab-checks"');
		expect(html).not.toContain('data-testid="tab-compare-models"');
	});
});

describe("delete agent", () => {
	it("?delete=1 opens the confirmation for an agent of this project", () => {
		state.configMissing = false;
		const html = page("?delete=1");
		expect(html).toContain('data-testid="delete-agent-dialog"');
		expect(html).toContain("Delete agent zz-test-loop?");
		expect(html).toContain("Its settings file is removed. Past runs, results and saved versions stay.");
		expect(html).toContain("Keep agent");
	});

	it("is not offered for a built-in agent (no settings file in the project)", () => {
		state.configMissing = true;
		const html = page("?delete=1");
		expect(html).not.toContain('data-testid="delete-agent-dialog"');
		state.configMissing = false;
	});

	it("is not offered to a viewer", () => {
		const viewer = { ...LOCAL_SESSION, authMode: "hosted", signedIn: true, role: "viewer" } as SessionInfo;
		const html = page("?delete=1", viewer);
		expect(html).not.toContain('data-testid="delete-agent-dialog"');
	});
});