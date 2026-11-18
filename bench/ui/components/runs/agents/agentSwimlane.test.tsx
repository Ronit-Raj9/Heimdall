import "@/lib/i18n";

import type { AgentHandoff, AgentSpan, AgentStats } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentSwimlane } from "./agentSwimlane";

const member = (agent_id: string, role: string): AgentStats => ({
	agent_id,
	role,
	depth: 0,
	source: "gateway_key",
	strength: "proven",
	steps: 1,
	model_turns: 1,
	tool_calls: 0,
	input_tokens: null,
	output_tokens: null,
	cost_usd: null,
	messages_sent: 0,
	messages_received: 0,
	busy_ms: 0,
});

const spans: AgentSpan[] = [
	{ agent_id: "lead", seq: 1, kind: "model_turn", start_ms: 0, end_ms: 4000, label: "planned", critical: true },
	{ agent_id: "worker", seq: 2, kind: "command", start_ms: 1000, end_ms: 9000, label: "<img src=x onerror=alert(1)>", is_error: true },
	{ agent_id: "worker", seq: 3, kind: "mystery_kind", start_ms: 9000, end_ms: 10000, label: "" },
];

const render = (over: Partial<React.ComponentProps<typeof AgentSwimlane>> = {}) =>
	renderToStaticMarkup(
		<AgentSwimlane
			spans={spans}
			members={[member("lead", "lead"), member("worker", "worker")]}
			wallMs={10000}
			busyMs={13000}
			criticalMs={4000}
			arm="A"
			{...over}
		/>,
	);

const handoffs: AgentHandoff[] = [
	{ index: 1, kind: "message", from: "lead", to: "worker", at_ms: 2500, bytes: 10, source: "gateway_key" },
	{
		index: 2,
		kind: "return",
		from: "worker",
		to: "lead",
		at_ms: 9500,
		bytes: 10,
		source: "self_report",
		facts: [{ fact: "f", sent: true, delivered: false }],
	},
	{ index: 3, kind: "message", from: "lead", to: "<b>ghost</b>", at_ms: 3000, bytes: 1, source: "gateway_key" },
	{ index: 4, kind: "message", from: "lead", to: "worker", at_ms: null, bytes: 1, source: "gateway_key" },
	{ index: 5, kind: "claim", from: "worker", at_ms: 4000, bytes: 1, source: "gateway_key" },
];

describe("AgentSwimlane", () => {
	it("shows wall, busy, the parallelism factor to one decimal and the critical path", () => {
		const html = render();
		expect(html).toContain('data-testid="agents-stat-wall-A"');
		expect(html).toContain("10 sec");
		expect(html).toContain("13 sec");
		expect(html).toContain("1.3x");
		expect(html).toContain("40% of wall");
	});

	it("says the parallelism factor is unknown without a wall time", () => {
		const html = render({ wallMs: 0 });
		expect(html).toMatch(/data-testid="agents-stat-parallelism-A".*data-state="unknown"/);
		expect(html).not.toContain("x</dd>");
	});

	it("draws one labelled lane per agent and positions blocks over the wall time", () => {
		const html = render();
		expect(html).toContain('data-testid="agents-lane-A-lead" data-critical="true"');
		expect(html).toContain('data-testid="agents-lane-A-worker" data-critical="false"');
		expect(html).toContain('aria-label="Lane of lead"');
		expect(html).toContain("left:10%;width:80%");
	});

	it("rings critical blocks and marks errors, with a legend for both", () => {
		const html = render();
		const critical = html.match(/<span role="img"[^>]*data-testid="agents-block-A-1"[^>]*>/)?.[0] ?? "";
		expect(critical).toContain('data-critical="true"');
		expect(critical).toContain("ring-2");
		expect(critical).toContain("critical path");
		const error = html.match(/<span role="img"[^>]*data-testid="agents-block-A-2"[^>]*>/)?.[0] ?? "";
		expect(error).toContain('data-error="true"');
		expect(error).toContain("border-chart-error");
		expect(error).not.toContain("ring-2");
		expect(html).toContain('data-testid="agents-legend-A"');
		expect(html).toContain("model turn");
		expect(html).toContain("mystery_kind");
	});

	it("draws handoff markers between the sender's and receiver's lanes at their time", () => {
		const html = render({ handoffs });
		expect(html).toContain('data-testid="agents-handoff-markers-A"');
		const down = html.match(/<span role="img"[^>]*data-testid="agents-handoff-marker-A-1"[^>]*>/)?.[0] ?? "";
		expect(down).toContain('aria-label="message #1: lead to worker, at 2.5 sec"');
		expect(down).toContain("left:25%;top:1rem;height:2rem");
		expect(down).toContain('data-direction="down"');
		expect(down).toContain('data-warn="false"');
		const up = html.match(/<span role="img"[^>]*data-testid="agents-handoff-marker-A-2"[^>]*>/)?.[0] ?? "";
		expect(up).toContain('data-direction="up"');
		expect(up).toContain('data-warn="true"');
		expect(up).toContain("altered");
		expect(html).toContain('data-testid="agents-legend-A-handoff"');
		expect(html).toContain('data-testid="agents-legend-A-altered"');
	});

	it("skips handoffs with no time, no recipient or a recipient with no lane", () => {
		const html = render({ handoffs });
		for (const index of [3, 4, 5]) expect(html).not.toContain(`data-testid="agents-handoff-marker-A-${index}"`);
		expect(html).not.toContain("ghost");
		const none = render({ handoffs: [handoffs[3]] });
		expect(none).not.toContain('data-testid="agents-handoff-markers-A"');
		expect(none).not.toContain('data-testid="agents-legend-A-handoff"');
	});

	it("keeps agent labels inert", () => {
		const html = render();
		expect(html).not.toContain("<img");
		expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
	});

	it("says when no row carries an agent id", () => {
		const html = render({ spans: [] });
		expect(html).toContain('data-testid="agents-swimlane-A-empty"');
		expect(html).not.toContain('data-testid="agents-legend-A"');
	});
});