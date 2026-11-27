import "@/lib/i18n";

import { LOCAL_SESSION, type SessionInfo } from "@/lib/session/sessionContext";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import AgentDetailView, { deleteDialogOpen } from "./agentDetailView";

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
vi.mock("@/components/agents/skillScanBar", () => ({ SkillScanBar: () => null })); // L5 scan bar has its own test

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
	// M2-41: a pasted ?delete=1 never opens the dialog; the menu pick on the page does.
	it("a pasted ?delete=1 opens no confirmation", () => {
		state.configMissing = false;
		const html = page("?delete=1");
		expect(html).not.toContain('data-testid="delete-agent-dialog"');
	});

	it("opens only when the menu set the param on this page", () => {
		expect(deleteDialogOpen("1", false)).toBe(true);
		expect(deleteDialogOpen("1", true)).toBe(false);
		expect(deleteDialogOpen(null, false)).toBe(false);
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

describe("agent page heading (M2-30)", () => {
	it("the h1 holds the agent name only; the version chip sits outside it", () => {
		const html = page("");
		const h1 = html.match(/<h1[^>]*>([\s\S]*?)<\/h1>/)?.[1] ?? "";
		expect(h1.replace(/<[^>]+>/g, "")).toBe("zz-test-loop");
		expect(html).toContain('data-testid="agent-detail-version"');
	});
});
