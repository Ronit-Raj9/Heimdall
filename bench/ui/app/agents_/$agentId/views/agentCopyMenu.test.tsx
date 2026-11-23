import "@/lib/i18n";

import { LOCAL_SESSION, type SessionInfo } from "@/lib/session/sessionContext";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import AgentDetailView from "./agentDetailView";

// The agent page's Duplicate and Rename actions (fix wave 2, N156).
const state = vi.hoisted(() => ({ configMissing: false }));

vi.mock("@tanstack/react-router", async () => {
	const { routerMock } = await import("@/lib/testing/routerMock");
	return { ...routerMock, useParams: () => ({ agentId: "zz-test-loop" }) };
});
vi.mock("@/components/agents/overflowMenu", () => ({
	OverflowMenu: ({ items }: { items: { label: string; testId?: string }[] }) => (
		<ul>
			{items.map((i) => (
				<li key={i.label} data-testid={i.testId}>
					{i.label}
				</li>
			))}
		</ul>
	),
}));
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
vi.mock("@/lib/store/apis/agentDeleteApi", () => ({ useDeleteAgentMutation: () => mutation(), useCopyAgentMutation: () => mutation() }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => okQuery({ agents: [{ id: "zz-test-loop" }] }),
}));
vi.mock("./checksTab", () => ({ default: () => null }));
vi.mock("@/components/modelswitch/compareModelsForm", () => ({ CompareModelsForm: () => null }));
vi.mock("./agentRunsTab", () => ({ default: () => null }));
vi.mock("./configTab", () => ({ default: () => null }));
vi.mock("./pullRequestsTab", () => ({ default: () => null }));
vi.mock("./reportCardTab", () => ({ default: () => null }));
vi.mock("./toolsTab", () => ({ default: () => null }));
vi.mock("./versionsTab", () => ({ default: () => null }));
vi.mock("@/components/sharedpool/imageRequests", () => ({ AgentImageApproval: () => null }));
vi.mock("../../new/views/connectForm", () => ({ SavedEndpointTest: () => null }));
vi.mock("@/components/agents/skillScanBar", () => ({ SkillScanBar: () => null })); // L5 scan bar has its own test

function page(search: string, session: SessionInfo = LOCAL_SESSION) {
	return render(
		<NuqsTestingAdapter searchParams={search}>
			<AgentDetailView />
		</NuqsTestingAdapter>,
		session,
	);
}

describe("duplicate and rename agent", () => {
	it("the menu offers Duplicate and Rename for an agent of this project", () => {
		state.configMissing = false;
		const html = page("");
		expect(html).toContain('data-testid="agent-detail-duplicate"');
		expect(html).toContain("Duplicate agent");
		expect(html).toContain('data-testid="agent-detail-rename"');
		expect(html).toContain("Rename agent");
	});

	it("?copy=duplicate and ?copy=rename open the dialog", () => {
		state.configMissing = false;
		expect(page("?copy=duplicate")).toContain("Duplicate agent zz-test-loop");
		const html = page("?copy=rename");
		expect(html).toContain('data-testid="copy-agent-dialog"');
		expect(html).toContain("Rename agent zz-test-loop");
	});

	it("is hidden for a built-in agent (no settings file in the project)", () => {
		state.configMissing = true;
		const html = page("?copy=duplicate");
		expect(html).not.toContain('data-testid="agent-detail-duplicate"');
		expect(html).not.toContain('data-testid="agent-detail-rename"');
		expect(html).not.toContain('data-testid="copy-agent-dialog"');
		state.configMissing = false;
	});

	it("is hidden for a viewer", () => {
		const viewer = { ...LOCAL_SESSION, authMode: "hosted", signedIn: true, role: "viewer" } as SessionInfo;
		const html = page("?copy=rename", viewer);
		expect(html).not.toContain('data-testid="agent-detail-rename"');
		expect(html).not.toContain('data-testid="copy-agent-dialog"');
	});
});