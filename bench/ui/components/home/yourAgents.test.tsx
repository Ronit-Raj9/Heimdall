import "@/lib/i18n";

import type { AgentSummary } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { YourAgents, agentCardStats, agentsWithResults, ownAgents } from "./yourAgents";

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

const evaluation = (id: string, agentId: string, created_at = "2026-11-10T00:00:00Z") =>
	({ id, status: "done", created_at, spec: { arms: [{ agent: agentId }] } }) as never;

const agent = (id: string) => ({ id, adapter: "command", scripted_kind: "", path: `agents/${id}.yaml` }) as AgentSummary;

describe("Home: your agents", () => {
	it("shows version, score per capability, cost per solved task, drop under faults and last evaluation", () => {
		const html = renderToStaticMarkup(
			<YourAgents agents={[agent("codex"), agent("support-bot"), agent("draft-bot")]} evaluations={[evaluation("ev_9", "support-bot")]} mayRun />,
		);
		expect(html).not.toContain("home-agent-codex");
		expect(html).toContain('data-testid="home-agent-support-bot-version">v4');
		expect(html).toContain("62%");
		expect(html).toContain('data-testid="home-agent-support-bot-cost">$0.20');
		expect(html).toContain("-32 pts (Broken tool)");
		expect(html).toContain("Last evaluation ");
		expect(html).toContain('data-testid="home-agent-draft-bot-none"');
	});

	it("offers to add an agent when the org has none of its own", () => {
		const html = renderToStaticMarkup(<YourAgents agents={[agent("codex")]} evaluations={[]} mayRun />);
		expect(html).toContain('data-testid="home-agents-empty"');
		expect(html).toContain('data-testid="home-agents-add"');
	});
});

describe("Home: which agents show (M2-11, M2-12, M1-19, X-13)", () => {
	it("adds built-in agents that were evaluated, newest evaluation first", () => {
		const list = [agent("support-bot"), agent("codex"), agent("draft-bot"), agent("zz-test-a")];
		const evs = [evaluation("ev_1", "support-bot", "2026-11-01T00:00:00Z"), evaluation("ev_2", "codex", "2026-11-20T00:00:00Z")];
		expect(ownAgents(list, 6, evs).map((a) => a.id)).toEqual(["codex", "support-bot", "draft-bot"]);
		expect(ownAgents(list).map((a) => a.id)).toEqual(["support-bot", "draft-bot"]);
		expect(agentsWithResults(list, evs)).toBe(2);
	});

	it("lists agents with no report card as short rows with a ghost Evaluate, and points to built-in agents", () => {
		const html = renderToStaticMarkup(
			<YourAgents agents={[agent("codex"), agent("draft-bot"), agent("other-bot")]} evaluations={[]} mayRun />,
		);
		expect(html).toContain('data-testid="home-agents-unevaluated"');
		expect(html).toContain("None of your 2 agents has a report card yet");
		expect(html).toContain('data-testid="home-agents-builtin-codex"');
		expect(html).toContain('data-testid="home-agents-scheduled"');
		expect(html).not.toContain('data-testid="home-agents-list"');
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