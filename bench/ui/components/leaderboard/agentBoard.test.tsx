import "@/lib/i18n";

import { agentLeaderboardRequest, type AgentLeaderboardRow } from "@/lib/store/apis/agentLeaderboardApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { nextTableSort, sortRows, type TableSort } from "@/components/ui/useTableSort";
import { AgentBoardTable, AgentBoardToolbar, agentFirstDir, agentSortAccessors, scoreKey } from "./agentBoard";
import { ALL, filterParam, formatCost, formatShare, reportCardSearch } from "./agentBoard.utils";

const rows: AgentLeaderboardRow[] = [
	{
		agent: "codex",
		version: 2,
		model: "gpt-5.4-mini",
		label: "notes",
		baseline: true,
		rank: 1,
		capabilities: { coding: { score: 0.415, ci_low: 0.3, ci_high: 0.52, runs: 30, tasks: 10, ranked: true } },
		cost_usd: 0.1234,
		runs: 30,
		last_evaluation: "",
	},
	{
		agent: "my-agent",
		version: 0,
		model: "deepseek-chat",
		label: "A",
		baseline: false,
		capabilities: { terminal: { score: 1, ci_low: 0.6, ci_high: 1, runs: 6, tasks: 3, ranked: false, note: "not enough runs" } },
		cost_usd: null,
		cost_runs: 2,
		cost_note: "no price for model deepseek-chat",
		runs: 6,
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

	it("marks small samples, ranks only with a capability, and never shows $0 for unpriced runs", () => {
		const html = renderToStaticMarkup(
			<AgentBoardTable
				rows={rows}
				capabilities={["terminal", "coding"]}
				rankBy="coding"
				minRuns={10}
				minTasks={5}
				onOpen={() => undefined}
			/>,
		);
		expect(html).toContain(">Rank<");
		expect(html.match(/data-testid="agent-board-rank"/g)?.length).toBe(2);
		expect(html).toContain('data-testid="agent-board-not-enough"');
		expect(html).toContain("30 runs, 10 tasks");
		expect(html).toContain("ranking needs 10 runs over 5 tasks");
		expect(html).toContain('data-testid="agent-board-cost-unknown"');
		expect(html).toContain("4 of 6 runs unpriced");
		expect(html).toContain("no price for model deepseek-chat");
		expect(html).not.toContain("$0<");
		const plain = renderToStaticMarkup(<AgentBoardTable rows={rows} capabilities={["coding"]} onOpen={() => undefined} />);
		expect(plain).not.toContain(">Rank<");
	});

	it("labels a simulated-customer row and keys it apart from the scripted row", () => {
		const sim: AgentLeaderboardRow = {
			...rows[0],
			baseline: false,
			capabilities: { support: { score: 0.5, ci_low: 0.2, ci_high: 0.8, runs: 8 } },
			customer: "simulated customer",
			simulator: "anthropic/claude-sim@v2",
		};
		const html = renderToStaticMarkup(
			<AgentBoardTable rows={[rows[0], sim]} capabilities={["support", "coding"]} onOpen={() => undefined} />,
		);
		expect(html.match(/data-testid="agent-board-simulated-customer"/g)?.length).toBe(1);
		expect(html).toContain(">simulated customer<");
		expect(html).toContain("anthropic/claude-sim@v2");
		expect(html).toContain("50%");
	});

	it("sorts by a clicked column and reverses on the second click (cost per run)", () => {
		const cheap: AgentLeaderboardRow = { ...rows[0], agent: "cheap", version: 1, cost_usd: 0.01 };
		const all = [rows[0], rows[1], cheap];
		const accessors = agentSortAccessors(["coding", "terminal"]);
		const order = (sort: TableSort<string>) => sortRows(all, sort, accessors).map((r) => r.agent);
		const once = nextTableSort<string>({ key: null, dir: "asc" }, "cost", agentFirstDir);
		expect(once).toEqual({ key: "cost", dir: "asc" });
		// A cost that is not known sorts last in either direction.
		expect(order(once)).toEqual(["cheap", "codex", "my-agent"]);
		const twice = nextTableSort(once, "cost", agentFirstDir);
		expect(twice).toEqual({ key: "cost", dir: "desc" });
		expect(order(twice)).toEqual(["codex", "cheap", "my-agent"]);
		// Scores start best first.
		expect(nextTableSort<string>({ key: "cost", dir: "asc" }, scoreKey("coding"), agentFirstDir).dir).toBe("desc");

		const html = renderToStaticMarkup(
			<AgentBoardTable
				rows={sortRows(all, twice, accessors)}
				capabilities={["coding"]}
				onOpen={() => undefined}
				head={(key) => ({ active: key === twice.key, dir: twice.dir, onSort: () => undefined })}
			/>,
		);
		expect(html).toMatch(/aria-sort="descending"[^>]*data-testid="agent-board-col-cost"/);
		expect(html).toContain('data-testid="agent-board-col-cost-sort"');
		expect(html).toContain('aria-label="Sort by Cost per run, sorted descending"');
		expect(html.match(/aria-sort="none"/g)?.length).toBe(4);
		expect(html.indexOf("agent-board-row-codex")).toBeLessThan(html.indexOf("agent-board-row-cheap"));
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
		expect(agentLeaderboardRequest({ capability: "coding", model: "" })).toEqual({
			url: "/leaderboard/agents",
			params: { capability: "coding" },
		});
	});
});
