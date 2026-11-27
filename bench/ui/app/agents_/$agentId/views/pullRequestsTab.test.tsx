import "@/lib/i18n";

import { SessionContext } from "@/lib/session/sessionContext";
import { ciWorkflowParams } from "@/lib/store/apis/integrationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import PullRequestsTab, { ciPanelParams } from "./pullRequestsTab";

const state = vi.hoisted(() => ({ lastParams: undefined as unknown, live: 4 as number | undefined }));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({ data: { agent: "my-agent", versions: [], live: state.live } }),
	useGetEvaluationPacksQuery: () => ({ data: [{ id: "coding", name: "Coding agent", benchmarks: ["hd-harbor-tb2"], capability: "coding" }] }),
}));

vi.mock("@/lib/store/apis/integrationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/integrationsApi")>()),
	useGetCIWorkflowQuery: (params: unknown) => {
		state.lastParams = params;
		return { data: { path: ".github/workflows/heimdall-evaluate.yml", mode: "hosted", workflow: "name: Heimdall evaluation (my-agent)\n" } };
	},
}));

vi.mock("@/lib/store/apis/platformApi", () => ({
	useCreateApiTokenMutation: () => [vi.fn(), { isLoading: false }],
}));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

describe("test on every pull request", () => {
	it("builds the hosted workflow query from the panel choices", () => {
		const p = ciPanelParams(
			"my-agent",
			{ provider: "gitlab", source: "config", config: " agents/my-agent.yaml ", pack: "coding", faults: ["model-down", "broken-tool"], failInconclusive: true },
			"https://h.example.com",
		);
		expect(p).toMatchObject({
			mode: "hosted",
			agent: "my-agent",
			provider: "gitlab",
			config: "agents/my-agent.yaml",
			faults: "model-down,broken-tool",
			fail_on: "regressed,inconclusive,failed",
			url: "https://h.example.com",
		});
		const image = ciPanelParams("a", { provider: "github", source: "image", config: "x", pack: "coding", faults: [], failInconclusive: false }, "");
		expect(ciWorkflowParams(image)).toEqual({ mode: "hosted", agent: "a", provider: "github", source: "image", pack: "coding", fail_on: "regressed,failed" });
	});

	it("shows the live version, the setup steps and the workflow file (local server)", () => {
		state.live = 4;
		const html = renderToStaticMarkup(<PullRequestsTab agentId="my-agent" />);
		expect(html).toContain("Test on every pull request");
		expect(html).toContain("v4");
		expect(html).toContain('data-testid="agent-ci-token-local"');
		expect(html).toContain("HEIMDALL_URL");
		expect(html).toContain(".github/workflows/heimdall-evaluate.yml");
		expect(html).toContain("Heimdall evaluation (my-agent)");
		expect(state.lastParams).toMatchObject({ mode: "hosted", agent: "my-agent", pack: "coding", faults: "model-down" });
		expect(html).not.toMatch(/[–—]/);
	});

	it("offers a CI token to an admin on a hosted server", () => {
		const session = { authMode: "hosted", status: "ready", signedIn: true, role: "admin", kind: "user" } as never;
		const html = renderToStaticMarkup(
			<SessionContext.Provider value={session}>
				<PullRequestsTab agentId="my-agent" />
			</SessionContext.Provider>,
		);
		expect(html).toContain('data-testid="agent-ci-create-token"');
		expect(html).toContain('data-testid="agent-ci-tokens-link"');
	});

	it("says when the agent has no saved version", () => {
		state.live = undefined;
		expect(renderToStaticMarkup(<PullRequestsTab agentId="my-agent" />)).toContain("No saved version yet");
	});
});
