import "@/lib/i18n";

import type { AgentSummary } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { YourAgents, agentCardStats } from "./yourAgents";

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({ data: { agent: "support-bot", versions: [{ version: 1 }, { version: 4 }] } }),
	useGetAgentReportCardQuery: ({ agent }: { agent: string }) =>
		agent === "support-bot"
			? {
					isLoading: false,
					data: {
						evaluation: "ev_9",
						arms: [{ label: "A", agent, model: "gpt-x" }],
						capabilities: [{ capability: "support", benchmarks: ["b"], arms: { A: { score: 0.62, ci_low: 0.5, ci_high: 0.7, runs: 10 } } }],
						benchmarks: [
							{
								benchmark: "b",
								capability: "support",
								experiment_id: "e",
								arms: { A: { score: 0.5, ci_low: 0, ci_high: 1, runs: 10, cost_usd: 0.1 } },
							},
						],
						reliability: [{ fault: "broken-tool", arms: { A: { score: 0.3, drop: -0.32 } } }],
					},
				}
			: { isLoading: false, data: { evaluation: "", arms: [], capabilities: [], benchmarks: [], reliability: [] } },
}));

vi.mock("@tanstack/react-router", () => ({
	Link: ({
		to,
		search: _s,
		params: _p,
		children,
		...rest
	}: {
		to: string;
		search?: unknown;
		params?: unknown;
		children: React.ReactNode;
	}) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

const agent = (id: string) => ({ id, adapter: "command", scripted_kind: "", path: `agents/${id}.yaml` }) as AgentSummary;

describe("Home: your agents", () => {
	it("shows version, score per capability, cost per solved task, drop under faults and last evaluation", () => {
		const html = renderToStaticMarkup(
			<YourAgents agents={[agent("codex"), agent("support-bot"), agent("draft-bot")]} evaluations={[]} mayRun />,
		);
		expect(html).not.toContain("home-agent-codex");
		expect(html).toContain('data-testid="home-agent-support-bot-version">v4');
		expect(html).toContain("62%");
		expect(html).toContain('data-testid="home-agent-support-bot-cost">$0.20');
		expect(html).toContain("-32 pts (Broken tool)");
		expect(html).toContain("Last evaluation ev_9");
		expect(html).toContain('data-testid="home-agent-draft-bot-none"');
	});

	it("offers to add an agent when the org has none of its own", () => {
		const html = renderToStaticMarkup(<YourAgents agents={[agent("codex")]} evaluations={[]} mayRun />);
		expect(html).toContain('data-testid="home-agents-empty"');
		expect(html).toContain('data-testid="home-agents-add"');
	});
});

describe("agentCardStats worst drop (N126)", () => {
	const report = (drops: number[]) =>
		({
			evaluation: "ev",
			arms: [{ label: "A", agent: "a", model: "m" }],
			capabilities: [],
			benchmarks: [],
			reliability: drops.map((drop, i) => ({ fault: `f${i}`, arms: { A: { score: 0.5, drop } } })),
		}) as unknown as Parameters<typeof agentCardStats>[0];

	it("picks the largest loss (most negative change), not the largest gain", () => {
		const s = agentCardStats(report([0.1, -0.3, -0.05]));
		expect(s.worstDrop).toEqual({ fault: "f1", drop: -0.3 });
		expect(s.faultsTested).toBe(true);
	});

	it("has no drop when every fault left the score the same or better", () => {
		const s = agentCardStats(report([0.1, 0]));
		expect(s.worstDrop).toBeNull();
		expect(s.faultsTested).toBe(true);
		expect(agentCardStats(report([])).faultsTested).toBe(false);
	});
});