import "@/lib/i18n";

import type { AgentSummary } from "@/lib/types/bench";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AgentConfigsTab } from "./agentConfigsTab";
import { initials, isTestAgent } from "./agentDisplay";

vi.mock("./agentJobs", () => ({
	useAgentJobs: () => [{ id: "j1", kind: "agent.check", status: "succeeded", params: { agent: "agents/codex.yaml" } }],
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...(rest as object)}>{children}</a>,
}));

const base = { image: "", dockerfile: "", base_arg: "", per_suite_build: false, parser: "", evidence_level: "gateway", notes_project_file: "", notes_user_file: "", env_names: [], file_paths: [], budgets: { wall_s: 1, cpus: 1, mem_gb: 1 }, gateway: { provider: "openai" } };
const agents = [
	{ ...base, id: "codex", path: "agents/codex.yaml", adapter: "command", model: "gpt-5", scripted_kind: "", sealed_runs: 3 },
	{ ...base, id: "oracle", path: "agents/oracle.yaml", adapter: "scripted", model: "", scripted_kind: "oracle", sealed_runs: 0 },
] as unknown as AgentSummary[];

const render = () =>
	renderToStaticMarkup(
		<NuqsTestingAdapter>
			<AgentConfigsTab query={{ data: { agents } as never, isLoading: false, isFetching: false, isError: false, refetch: () => undefined }} />
		</NuqsTestingAdapter>,
	);

describe("AgentConfigsTab", () => {
	it("shows agents as cards with a friendly adapter, model chip, check status and Run", () => {
		const html = render();
		expect(html).toContain('data-testid="agents-row-codex"');
		expect(html).toContain("Runs as a command-line program");
		expect(html).toContain("gpt-5");
		expect(html).toContain('data-testid="agents-row-codex-status"');
		expect(html).toContain("Works");
		expect(html).toContain('data-testid="agents-row-codex-run"');
	});

	it("folds scripted agents into a Test and demo agents group", () => {
		const html = render();
		expect(html).toContain('data-testid="agents-test-group"');
		expect(html).toContain("Test and demo agents (used by the example benchmarks)");
		const group = html.slice(html.indexOf('data-testid="agents-test-group"'));
		expect(group).toContain('data-testid="agents-row-oracle"');
		expect(isTestAgent({ id: "toy", scripted_kind: "", adapter: "command" })).toBe(true);
		for (const id of ["voice-broken", "unsafe", "storm", "broken", "exploit", "refuse", "a2a-broken", "codex-task", "live-l10-codex-task"]) {
			expect(isTestAgent({ id, scripted_kind: "", adapter: "command" })).toBe(true);
		}
		expect(isTestAgent({ id: "helper", scripted_kind: "", adapter: "command", description: "Test double for the example benchmarks" })).toBe(true);
		expect(isTestAgent({ id: "codex", scripted_kind: "", adapter: "command" })).toBe(false);
		expect(isTestAgent({ id: "claude-code", scripted_kind: "", adapter: "command", description: "Anthropic CLI agent" })).toBe(false);
		expect(initials("claude-code")).toBe("CC");
	});
});
