import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import AgentDetailView from "./agentDetailView";

vi.mock("@tanstack/react-router", async () => {
	const { routerMock } = await import("@/lib/testing/routerMock");
	return { ...routerMock, useParams: () => ({ agentId: "no-such-agent" }) };
});
vi.mock("./agentOps.shared", () => ({ useAgentSummary: () => ({ agent: undefined, isLoading: false }) }));
vi.mock("../../../agents/views/agentJobs", () => ({ useAgentJobs: () => [] }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useGetAgentCompatibilityQuery: () => ({ data: { capabilities: [] } }),
	useGetAgentVersionsQuery: () => ({ data: { versions: [] } }),
}));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({ useGetAgentConfigQuery: () => okQuery({ id: "no-such-agent" }) }));
vi.mock("@/lib/store/apis/agentDeleteApi", () => ({ useDeleteAgentMutation: () => mutation() }));
vi.mock("./checksTab", () => ({ default: () => <p data-testid="stub-checks" /> }));
vi.mock("@/components/modelswitch/compareModelsForm", () => ({ CompareModelsForm: () => null }));
vi.mock("./agentRunsTab", () => ({ default: () => null }));
vi.mock("./configTab", () => ({ default: () => null }));
vi.mock("./pullRequestsTab", () => ({ default: () => null }));
vi.mock("./reportCardTab", () => ({ default: () => <p data-testid="stub-report-card" /> }));
vi.mock("./toolsTab", () => ({ default: () => null }));
vi.mock("./versionsTab", () => ({ default: () => null }));
vi.mock("@/components/sharedpool/imageRequests", () => ({ AgentImageApproval: () => null }));
vi.mock("../../new/views/connectForm", () => ({ SavedEndpointTest: () => null }));
vi.mock("@/components/agents/skillScanBar", () => ({ SkillScanBar: () => null }));

describe("agent page for an unknown agent (N86)", () => {
	it("unknown id shows not found", () => {
		const html = render(
			<NuqsTestingAdapter searchParams="">
				<AgentDetailView />
			</NuqsTestingAdapter>,
			LOCAL_SESSION,
		);
		expect(html).toContain('data-testid="agent-not-found"');
		expect(html).toContain("There is no agent named no-such-agent.");
		expect(html).toContain('href="/agents"');
		expect(html).toContain("Back to agents");
		expect(html).not.toContain('data-testid="stub-report-card"');
		expect(html).not.toContain('data-testid="agent-detail-evaluate"');
	});
});