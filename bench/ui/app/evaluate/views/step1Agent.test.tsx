import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { EMPTY_DRAFT, EvaluateDraftProvider, type EvaluateDraft } from "./evaluateDraft";
import type { AgentWithCaps } from "./evaluate.utils";
import { Step1Agent } from "./step1Agent";

const agent = (id: string, extra: Partial<AgentWithCaps> = {}) =>
	({ id, adapter: "command", model: "gpt-x", scripted_kind: "", ...extra }) as AgentWithCaps;

vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<object>()),
	useGetAgentsQuery: () => ({
		isLoading: false,
		isError: false,
		data: { agents: [agent("zz-test-loop", { adapter: "remote_http" }), agent("codex"), agent("storm")] },
		refetch: () => undefined,
	}),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<object>()),
	useGetAgentVersionsQuery: () => ({
		isLoading: false,
		data: {
			agent: "zz-test-loop",
			versions: [
				{ version: 1, runs: 3 },
				{ version: 2, runs: 0 },
			],
		},
	}),
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...p }: { children: React.ReactNode; "data-testid"?: string }) => <a data-testid={p["data-testid"]}>{children}</a>,
}));

const render = (initial: Partial<EvaluateDraft>) =>
	renderToStaticMarkup(
		<EvaluateDraftProvider initial={{ ...EMPTY_DRAFT, ...initial }}>
			<Step1Agent />
		</EvaluateDraftProvider>,
	);

describe("Step1Agent", () => {
	it("shows the version select in a selected agent bar above the agent grids (N94)", () => {
		const html = render({ agent: "zz-test-loop" });
		const bar = html.indexOf('data-testid="evaluate-selected-agent"');
		const version = html.indexOf('data-testid="evaluate-version"');
		expect(bar).toBeGreaterThan(-1);
		expect(version).toBeGreaterThan(bar);
		expect(version).toBeLessThan(html.indexOf('data-testid="evaluate-agents-own-option-zz-test-loop"'));
		expect(version).toBeLessThan(html.indexOf('data-testid="evaluate-mcp-server"'));
		expect(html).toContain("zz-test-loop selected");
		expect(html).toContain("Version of zz-test-loop");
		expect(html).toContain("This agent has 2 saved versions. Latest is v2.");
	});

	it("has no bar before an agent is picked", () => {
		const html = render({});
		expect(html).not.toContain('data-testid="evaluate-selected-agent"');
		expect(html).toContain("Pick an agent to continue.");
	});

	it("hides test agents without an unasked note (N97, M3-49)", () => {
		const html = render({});
		expect(html).not.toContain('data-testid="evaluate-agents-own-option-storm"');
		expect(html).not.toContain("Test agents such as oracle and noop are not listed");
		expect(html).toContain('type="radio"');
	});
});