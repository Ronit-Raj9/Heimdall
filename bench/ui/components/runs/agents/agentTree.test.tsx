import "@/lib/i18n";

import type { AgentStats, AgentTree as AgentTreeData } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentTree } from "./agentTree";

const stats = (agent_id: string, over: Partial<AgentStats> = {}): AgentStats => ({
	agent_id,
	depth: 0,
	source: "gateway_key",
	strength: "proven",
	steps: 1,
	model_turns: 1,
	tool_calls: 0,
	input_tokens: 1,
	output_tokens: 1,
	cost_usd: 0.01,
	messages_sent: 0,
	messages_received: 0,
	busy_ms: 1,
	...over,
});

const members: AgentStats[] = [
	stats("reviewer", { parent_id: "lead", role: "reviewer", source: "self_report", strength: "reported", exit: "exit code 137" }),
	stats("lead", { role: "lead", model: "claude-sonnet-4-5", exit: "completed" }),
	stats("worker-a", { parent_id: "lead", role: "<i>worker</i>", strength: "declared", source: "header", exit: "timeout" }),
	stats("stray", { parent_id: "gone" }),
];

const tree: AgentTreeData = {
	version: "1.0.0",
	shape: "team",
	agents: [{ id: "lead", name: "Team lead", depth: 0, source: "gateway_key", strength: "proven" }],
	coverage: { steps: 1, attributed: 1, unknown: 0 },
};

describe("AgentTree", () => {
	it("orders depth first and indents children", () => {
		const html = renderToStaticMarkup(<AgentTree members={members} tree={tree} arm="A" />);
		const order = [...html.matchAll(/data-testid="agents-tree-A-([^"-]+(?:-[a-z])?)" data-depth="(\d)"/g)].map((m) => `${m[1]}:${m[2]}`);
		expect(order).toEqual(["lead:0", "reviewer:1", "worker-a:1", "stray:0"]);
		expect(html).toContain("padding-left:1.5rem");
		expect(html).toContain("Team lead");
		expect(html).toContain("claude-sonnet-4-5");
	});

	it("badges strength and exit", () => {
		const html = renderToStaticMarkup(<AgentTree members={members} tree={tree} arm="A" />);
		expect(html).toMatch(/data-testid="agents-tree-A-lead-strength" data-tone="good"/);
		expect(html).toMatch(/data-testid="agents-tree-A-worker-a-strength" data-tone="neutral"/);
		expect(html).toMatch(/data-testid="agents-tree-A-reviewer-strength" data-tone="warn"/);
		expect(html).toMatch(/data-testid="agents-tree-A-lead-exit" data-tone="good"/);
		expect(html).toMatch(/data-testid="agents-tree-A-reviewer-exit" data-tone="bad"[^>]*>.*exit code 137/);
		expect(html).toMatch(/data-testid="agents-tree-A-worker-a-exit" data-tone="warn"/);
		expect(html).toContain("self-report");
	});

	it("notes an orphan and keeps roles inert", () => {
		const html = renderToStaticMarkup(<AgentTree members={members} arm="A" />);
		expect(html).toContain("Its parent is not in the tree");
		expect(html).not.toContain("<i>worker");
		expect(html).toContain("&lt;i&gt;worker&lt;/i&gt;");
	});

	it("falls back to the tree's nodes without members", () => {
		const html = renderToStaticMarkup(<AgentTree members={[]} tree={tree} arm="A" />);
		expect(html).toContain('data-testid="agents-tree-A-lead"');
		expect(renderToStaticMarkup(<AgentTree members={[]} arm="A" />)).toBe("");
	});
});