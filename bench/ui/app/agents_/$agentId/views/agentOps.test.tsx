import "@/lib/i18n";

import type { AgentMCPResponse, AgentSkillsResponse } from "@/lib/types/agentops";
import type { AgentSummary } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import en from "@/locales/en/agentops.json";
import { ChecksTab } from "./checksTab";
import { ToolsTab, skillNameFromFile } from "./toolsTab";

const state = vi.hoisted(() => ({
	skills: undefined as AgentSkillsResponse | undefined,
	mcp: undefined as AgentMCPResponse | undefined,
	agents: [] as Partial<AgentSummary>[],
	hosted: false,
}));

vi.mock("@/lib/store/apis/agentopsApi", () => ({
	useGetAgentSkillsQuery: () => ({ data: state.skills, isLoading: false }),
	useGetAgentMCPQuery: () => ({ data: state.mcp, isLoading: false }),
	useUploadAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	useScanAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	useDeleteAgentSkillMutation: () => [vi.fn(), { isLoading: false }],
	usePutAgentMCPMutation: () => [vi.fn(), { isLoading: false }],
}));

vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: state.agents }, isLoading: false }),
	getBenchErrorMessage: () => "error",
}));

vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { suites: [] } }),
	useGetSuiteQuery: () => ({ data: undefined }),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useListJobsQuery: () => ({ data: { jobs: [] } }) }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/components/platform/useJobLog", () => ({ useJobLog: () => ({ lines: [] }) }));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ authMode: state.hosted ? "hosted" : "local" }),
}));

vi.mock("@tanstack/react-router", () => ({
	useParams: () => ({}),
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

const codex: Partial<AgentSummary> = {
	id: "codex",
	path: "agents/codex.yaml",
	image: "bench/codex:1",
	dockerfile: "agents/images/codex/Dockerfile",
	per_suite_build: false,
};

describe("agent ops tabs", () => {
	it("checks tab offers check, build and diff jobs locally", () => {
		state.agents = [codex];
		state.hosted = false;
		const html = renderToStaticMarkup(<ChecksTab agentId="codex" />);
		expect(html).toContain('data-testid="job-run-agent.check"');
		expect(html).toContain('data-testid="job-run-agent.build"');
		expect(html).toContain('data-testid="job-run-agent.diff"');
		expect(html).toContain('value="coding-core"');
	});

	it("checks tab builds on a runner in hosted mode", () => {
		state.agents = [codex];
		state.hosted = true;
		const html = renderToStaticMarkup(<ChecksTab agentId="codex" />);
		expect(html).toContain('data-testid="job-run-agent.check"');
		expect(html).toContain('data-testid="job-run-agent.build"');
		expect(html).not.toContain('data-testid="job-run-agent.diff"');
		expect(html).toContain('data-testid="agent-build-runner-note"');
		// Y03: hosted agents can try one real task on their worker.
		expect(html).toContain('data-testid="agent-check-try-real-hint"');
		expect(html.split('data-testid="job-run-agent.check"').length - 1).toBe(2);
	});

	it("checks tab points to New agent when the agent is missing", () => {
		state.agents = [];
		const html = renderToStaticMarkup(<ChecksTab agentId="ghost" />);
		expect(html).toContain('data-testid="checks-no-agent"');
		expect(html).toContain("No agent &quot;ghost&quot; in this project");
		expect(html).toContain('href="/agents/new"');
	});

	it("tools tab lists skills with a scan button and the MCP servers", () => {
		state.hosted = false; // the Scan button is for members: a local session may use it
		state.skills = {
			agent: "codex",
			config_path: "agents/codex.yaml",
			dir: "agents/codex/skills",
			installed: true,
			skills: [{ name: "review", path: "agents/codex/skills/review", files: 2, bytes: 2048, valid: true }],
		};
		state.mcp = {
			agent: "codex",
			config_path: "agents/codex.yaml",
			format: "codex_toml",
			mount_path: "",
			servers_file: "agents/codex/mcp.yaml",
			servers_yaml: "services: []",
			servers: [{ id: "files", image: "x@sha256:abc" }],
		};
		const html = renderToStaticMarkup(<ToolsTab agentId="codex" />);
		expect(html).toContain("review");
		expect(html).toContain('data-testid="agent-skill-scan-review"');
		expect(html).toContain('data-testid="agent-mcp-server-list"');
		expect(html).toContain("files (version pinned)");
		expect(html).toContain('data-testid="job-run-scan.mcp"');
	});

	it("tools tab shows the empty skills state", () => {
		state.skills = { agent: "codex", config_path: "agents/codex.yaml", dir: "agents/codex/skills", installed: false, skills: [] };
		expect(renderToStaticMarkup(<ToolsTab agentId="codex" />)).toContain('data-testid="agent-skills-empty"');
	});

	it("reads every visible string from locales/en/agentops.json", () => {
		state.agents = [codex];
		state.hosted = false;
		const checks = renderToStaticMarkup(<ChecksTab agentId="codex" />);
		expect(checks).toContain(en.checks.check.title);
		expect(checks).toContain(
			en.checks.build.hintDockerfile.replace("{{image}}", "bench/codex:1").replace("{{dockerfile}}", "agents/images/codex/Dockerfile"),
		);
		expect(checks).toContain(en.checks.diff.open);
		const tools = renderToStaticMarkup(<ToolsTab agentId="codex" />);
		expect(tools).toContain(en.tools.skills.title);
		expect(tools).toContain(en.tools.mcp.title);
		expect(tools).toContain(en.tools.skills.upload);
		expect(tools).not.toMatch(/tools\.(skills|mcp)\./);
		expect(checks).not.toMatch(/checks\.(check|build|diff)\./);
	});

	it("derives a skill name from the zip name", () => {
		expect(skillNameFromFile("Code Review.zip")).toBe("Code-Review");
		expect(skillNameFromFile("__x.zip")).toBe("x");
	});
});