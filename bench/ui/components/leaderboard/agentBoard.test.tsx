import "@/lib/i18n";

import { agentLeaderboardRequest, type AgentLeaderboardRow } from "@/lib/store/apis/agentLeaderboardApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentBoardTable, AgentBoardToolbar } from "./agentBoard";
import { ALL, filterParam, formatCost, formatShare, reportCardSearch } from "./agentBoard.utils";

const rows: AgentLeaderboardRow[] = [
	{
		agent: "codex",
		version: 2,
		model: "gpt-5.4-mini",
		label: "notes",
		baseline: true,
		capabilities: { coding: { score: 0.415, ci_low: 0.3, ci_high: 0.52, runs: 30 } },
		cost_usd: 0.1234,
		last_evaluation: "",
	},
	{
		agent: "my-agent",
		version: 0,
		model: "deepseek-chat",
		label: "A",
		baseline: false,
		capabilities: { terminal: { score: 1, ci_low: 0.6, ci_high: 1, runs: 6 } },
		cost_usd: null,
		last_evaluation: "",
	},
];

describe("agent board", () => {
	it("renders rows with capability scores, ranges, cost and the baseline badge", () => {
		const html = renderToStaticMarkup(<AgentBoardTable rows={rows} capabilities={["terminal", "coding"]} onOpen={() => undefined} />);
		expect(html).toContain('data-testid="agent-board-row-codex"');
		expect(html).toContain("v2");
		expect(html).toContain("unsaved");
		expect(html).toContain("gpt-5.4-mini");
		expect(html).toContain("42%");
		expect(html).toContain("30% to 52%");
		expect(html).toContain("not run");
		expect(html).toContain("$0.12");
		expect(html).toContain(">Terminal<");
		expect(html).toContain(">Coding<");
		expect(html.match(/data-testid="agent-board-baseline"/g)?.length).toBe(1);
		expect(html).toContain(">baseline<");
		expect(html).toContain('role="link"');
	});

	it("shows the clear button only when filtered", () => {
		const base = { capabilities: ["coding"], models: ["m"], onCapability: () => undefined, onModel: () => undefined };
		const off = renderToStaticMarkup(<AgentBoardToolbar {...base} capability={ALL} model={ALL} />);
		expect(off).not.toContain("agent-board-reset");
		const on = renderToStaticMarkup(<AgentBoardToolbar {...base} capability="coding" model={ALL} onReset={() => undefined} />);
		expect(on).toContain("agent-board-reset");
	});

	it("formats and builds requests", () => {
		expect(formatShare(0.415)).toBe("42%");
		expect(formatCost(null)).toBe("-");
		expect(formatCost(0.001)).toBe("$0.0010");
		expect(formatCost(3.2)).toBe("$3.20");
		expect(filterParam(ALL)).toBeUndefined();
		expect(filterParam("coding")).toBe("coding");
		expect(reportCardSearch(rows[0])).toEqual({ tab: "report-card", version: "2", model: "gpt-5.4-mini" });
		expect(reportCardSearch(rows[1])).toEqual({ tab: "report-card", model: "deepseek-chat" });
		expect(agentLeaderboardRequest({})).toBe("/leaderboard/agents");
		expect(agentLeaderboardRequest({ capability: "coding", model: "" })).toEqual({ url: "/leaderboard/agents", params: { capability: "coding" } });
	});
});
