import "@/lib/i18n";

import type { AgentStats } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentTable } from "./agentTable";

const base: AgentStats = {
	agent_id: "lead",
	role: "lead",
	model: "claude-sonnet-4-5",
	depth: 0,
	source: "gateway_key",
	strength: "proven",
	steps: 7,
	model_turns: 3,
	tool_calls: 3,
	input_tokens: 18420,
	output_tokens: 2210,
	cost_usd: 0.0884,
	messages_sent: 3,
	messages_received: 2,
	busy_ms: 52200,
};

const members: AgentStats[] = [
	base,
	{
		...base,
		agent_id: "reviewer",
		role: "<b>reviewer</b>",
		model: undefined,
		parent_id: "lead",
		depth: 1,
		input_tokens: null,
		output_tokens: null,
		cost_usd: null,
		busy_ms: 850,
	},
];

describe("AgentTable", () => {
	it("renders counts, tokens, cost with four decimals and busy time", () => {
		const html = renderToStaticMarkup(<AgentTable members={members} arm="A" />);
		expect(html).toContain('data-testid="agents-table-A"');
		expect(html).toContain("Messages received");
		expect(html).toContain("18,420");
		expect(html).toContain('data-testid="agents-row-A-lead-cost">$0.0884');
		expect(html).toContain("52.2 sec");
		expect(html).toContain("850 ms");
	});

	it("shows unknown tokens and cost as unknown, never zero", () => {
		const html = renderToStaticMarkup(<AgentTable members={members} arm="A" />);
		for (const cell of ["input", "output", "cost"]) {
			expect(html).toMatch(new RegExp(`data-state="unknown" data-testid="agents-row-A-reviewer-${cell}">unknown<`));
		}
		expect(html).not.toContain("$0.0000");
		expect(html).toContain("not set");
	});

	it("keeps agent text inert", () => {
		const html = renderToStaticMarkup(<AgentTable members={members} arm="A" />);
		expect(html).not.toContain("<b>reviewer");
		expect(html).toContain("&lt;b&gt;reviewer&lt;/b&gt;");
	});
});