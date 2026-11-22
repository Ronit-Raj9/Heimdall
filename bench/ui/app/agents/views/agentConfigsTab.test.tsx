import "@/lib/i18n";

import type { AgentSummary } from "@/lib/types/bench";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { AgentConfigsTab, configsCount, splitAgents } from "./agentConfigsTab";
import { filterAgents, initials, isTestAgent, sortAgents } from "./agentDisplay";
import { FrameworksTab } from "./frameworksTab";

vi.mock("./agentJobs", () => ({
	useAgentJobs: () => [{ id: "j1", kind: "agent.check", status: "succeeded", params: { agent: "agents/codex.yaml" } }],
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...(rest as object)}>{children}</a>,
}));

const base = {
	image: "",
	dockerfile: "",
	base_arg: "",
	per_suite_build: false,
	parser: "",
	evidence_level: "gateway",
	notes_project_file: "",
	notes_user_file: "",
	env_names: [],
	file_paths: [],
	budgets: { wall_s: 1, cpus: 1, mem_gb: 1 },
	gateway: { provider: "openai" },
};
const agents = [
	{ ...base, id: "codex", path: "agents/codex.yaml", adapter: "command", model: "gpt-5", scripted_kind: "", sealed_runs: 3 },
	{ ...base, id: "support-bot", path: "agents/support-bot.yaml", adapter: "remote_http", model: "", scripted_kind: "", sealed_runs: 1 },
	{ ...base, id: "oracle", path: "agents/oracle.yaml", adapter: "scripted", model: "", scripted_kind: "oracle", sealed_runs: 0 },
] as unknown as AgentSummary[];

const render = (list: AgentSummary[] = agents, searchParams: Record<string, string> = {}) =>
	renderToStaticMarkup(
		<NuqsTestingAdapter searchParams={searchParams}>
			<AgentConfigsTab
				query={{ data: { agents: list } as never, isLoading: false, isFetching: false, isError: false, refetch: () => undefined }}
			/>
		</NuqsTestingAdapter>,
	);

const more = [
	...agents,
	{
		...base,
		id: "claude-code",
		path: "agents/claude-code.yaml",
		adapter: "command",
		model: "claude-sonnet",
		scripted_kind: "",
		sealed_runs: 9,
		runs_total: 9,
		last_run_at: "2026-11-01T10:00:00Z",
	},
	{
		...base,
		id: "my-plain-loop",
		path: "agents/my-plain-loop.yaml",
		adapter: "command",
		model: "gpt-6-luna",
		scripted_kind: "",
		sealed_runs: 0,
		runs_total: 4,
		last_run_at: "2026-11-21T10:00:00Z",
	},
	{
		...base,
		id: "live-l10-codex-task",
		path: "agents/live-l10-codex-task.yaml",
		adapter: "command",
		model: "",
		scripted_kind: "",
		sealed_runs: 0,
	},
] as unknown as AgentSummary[];

/** The data-testid of every agent card in render order. */
const cardIds = (html: string) =>
	[...html.matchAll(/data-testid="agents-row-([A-Za-z0-9_-]+?)"/g)].map((m) => m[1]).filter((id) => !/-(status|run|runs|last)$/.test(id));

describe("AgentConfigsTab", () => {
	it("shows agents as cards with a friendly adapter, model chip, check status and Evaluate", () => {
		const html = render();
		expect(html).toContain('data-testid="agents-row-codex"');
		expect(html).toContain("Runs as a command-line program");
		expect(html).toContain("gpt-5");
		expect(html).toContain('data-testid="agents-row-codex-status"');
		expect(html).toContain("Works");
		expect(html).toContain('data-testid="agents-row-codex-run"');
	});

	it("splits your agents from baseline agents and hides test agents", () => {
		const html = render();
		const yours = html.slice(html.indexOf('data-testid="agents-yours"'), html.indexOf('data-testid="agents-baselines"'));
		const baselines = html.slice(html.indexOf('data-testid="agents-baselines"'));
		expect(yours).toContain("Your agents");
		expect(yours).toContain('data-testid="agents-row-support-bot"');
		expect(yours).not.toContain('data-testid="agents-row-codex"');
		expect(baselines).toContain("Baseline agents");
		expect(baselines).toContain('data-testid="agents-row-codex"');
		expect(html).not.toContain("agents-row-oracle");
		expect(html).not.toContain("agents-test-group");
		const split = splitAgents(agents);
		expect(split.yours.map((a) => a.id)).toEqual(["support-bot"]);
		expect(split.baselines.map((a) => a.id)).toEqual(["codex"]);
		expect(split.tests.map((a) => a.id)).toEqual(["oracle"]);
		expect(isTestAgent({ id: "toy", scripted_kind: "", adapter: "command" })).toBe(true);
		for (const id of [
			"voice-broken",
			"unsafe",
			"storm",
			"broken",
			"exploit",
			"refuse",
			"a2a-broken",
			"codex-task",
			"live-l10-codex-task",
		]) {
			expect(isTestAgent({ id, scripted_kind: "", adapter: "command" })).toBe(true);
		}
		expect(
			isTestAgent({ id: "helper", scripted_kind: "", adapter: "command", description: "Test double for the example benchmarks" }),
		).toBe(true);
		expect(isTestAgent({ id: "codex", scripted_kind: "", adapter: "command" })).toBe(false);
		expect(isTestAgent({ id: "claude-code", scripted_kind: "", adapter: "command", description: "Anthropic CLI agent" })).toBe(false);
		expect(initials("claude-code")).toBe("CC");
	});

	it("has a search box and a sort select; the search narrows the cards by id, model or adapter", () => {
		const html = render(more);
		expect(html).toContain('data-testid="agents-search"');
		expect(html).toContain('data-testid="agents-sort"');
		expect(cardIds(render(more, { q: "plain" }))).toEqual(["my-plain-loop"]);
		expect(cardIds(render(more, { q: "SONNET" }))).toEqual(["claude-code"]);
		expect(cardIds(render(more, { q: "remote_http" }))).toEqual(["support-bot"]);
		const none = render(more, { q: "nothing-like-this" });
		expect(cardIds(none)).toEqual([]);
		expect(none).toContain('data-testid="agents-no-match"');
		expect(none).toContain("No agents match.");
		expect(none).toContain("Clear the search.");
		expect(filterAgents(more, "codex").map((a) => a.id)).toEqual(["codex", "live-l10-codex-task"]);
	});

	it("sorts by name, last run or runs", () => {
		expect(sortAgents(more, "name").map((a) => a.id)).toEqual([
			"claude-code",
			"codex",
			"live-l10-codex-task",
			"my-plain-loop",
			"oracle",
			"support-bot",
		]);
		expect(
			sortAgents(more, "last")
				.map((a) => a.id)
				.slice(0, 2),
		).toEqual(["my-plain-loop", "claude-code"]);
		expect(
			sortAgents(more, "runs")
				.map((a) => a.id)
				.slice(0, 3),
		).toEqual(["claude-code", "my-plain-loop", "codex"]);
		// The file order is support-bot then my-plain-loop; the default sort is by name.
		expect(cardIds(render(more))).toEqual(["my-plain-loop", "support-bot", "claude-code", "codex"]);
		expect(cardIds(render(more, { sort: "runs" }))).toEqual(["my-plain-loop", "support-bot", "claude-code", "codex"]);
		const late = more.map((a) => (a.id === "support-bot" ? { ...a, last_run_at: "2026-11-22T10:00:00Z" } : a));
		expect(cardIds(render(late, { sort: "last" })).slice(0, 2)).toEqual(["support-bot", "my-plain-loop"]);
	});

	it("counts in the tab badge exactly the cards the Configs tab shows", () => {
		expect(configsCount(more)).toBe(cardIds(render(more)).length);
		expect(configsCount(more)).toBe(4);
	});

	it("says scored runs and, when more were started, how many", () => {
		const html = render(more);
		expect(html).toMatch(/data-testid="agents-row-my-plain-loop-runs"[^>]*>0 scored runs \(4 started\)/);
		expect(html).toMatch(/data-testid="agents-row-claude-code-runs"[^>]*>9 scored runs</);
		expect(html).toMatch(/data-testid="agents-row-claude-code-last"[^>]*>Last run /);
	});

	it("hides test agents on the Frameworks tab until Show them", () => {
		const html = renderToStaticMarkup(
			<FrameworksTab
				query={{ data: { agents: more } as never, isLoading: false, isFetching: false, isError: false, refetch: () => undefined }}
			/>,
		);
		expect(html).not.toContain("frameworks-row-live-l10-codex-task");
		expect(html).not.toContain("frameworks-row-oracle");
		expect(html).toContain('data-testid="frameworks-tests-hidden"');
		expect(html).toContain("2 test agents hidden");
		expect(html).toContain("Show them");
	});
});