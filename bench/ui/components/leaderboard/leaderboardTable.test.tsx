import "@/lib/i18n";

import type { LeaderboardResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { DEFAULT_SORT, nextSort, sortEntries, type LeaderboardSort } from "./leaderboard.utils";
import { LeaderboardTable } from "./leaderboardTable";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const board = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/leaderboard.json"), "utf8")) as LeaderboardResponse;
const FAULTS = ["model-outage", "stale-notes", "test-crash"];

const render = (sort: LeaderboardSort = DEFAULT_SORT, faults: string[] = FAULTS) =>
	renderToStaticMarkup(
		<LeaderboardTable rows={sortEntries(board.entries, sort)} faults={faults} sort={sort} onSort={() => undefined} alpha={board.alpha} />,
	);

/** The markup of one data row. */
const row = (html: string, rank: number) => html.match(new RegExp(`data-testid="leaderboard-row-${rank}"[\\s\\S]*?</tr>`))?.[0] ?? "";

describe("LeaderboardTable", () => {
	it("shows one row per entry with rank, tier, agent and links to each experiment's verdict", () => {
		const html = render();
		expect(html).toContain('data-testid="leaderboard-table"');
		for (const e of board.entries) expect(html).toContain(`data-testid="leaderboard-row-${e.rank}"`);
		const leader = row(html, 1);
		expect(leader).toContain('data-tier="1"');
		expect(leader).toContain(">Top group<");
		expect(leader).toContain(">tied with leader<");
		expect(leader).toContain(">codex<");
		expect(leader).toContain(">gpt-5.4-mini<");
		expect(leader).toContain(">B: code-wins note<");
		expect(leader).toContain('href="/experiments/e_synthetic-codewins"');
		expect(row(html, 4)).toContain(">Behind<");
		expect(row(html, 4)).toContain(">clearly behind<");
	});

	it("puts the tier reason and the Holm-adjusted p in the tier badge's accessible name", () => {
		const html = render();
		expect(row(html, 4)).toContain('aria-label="Behind: p 0.012 on 8 common tasks. Holm-adjusted p vs leader: 0.012."');
		expect(row(html, 1)).toContain("Leader: every other entry is compared with this one.");
	});

	it("draws success with its interval and passes", () => {
		const leader = row(render(), 1);
		expect(leader).toContain('aria-label="Verified success 75%, 95% interval 52% to 98%"');
		expect(leader).toContain('data-testid="leaderboard-interval-1"');
		expect(leader).toContain("18 of 24 solved");
		expect(leader).toContain(">solved all 3 tries<");
	});

	it("never shows an unknown as 0: hatched reliability, n/a cost, unknown tool calls", () => {
		const html = render();
		const custom = row(html, 4);
		expect(custom).toMatch(/data-testid="leaderboard-reliability-4".*?data-state="unknown"/);
		expect(custom).toContain("bench-hatched");
		expect(custom).toContain(">solved all 1 tries<");
		const mini = row(html, 5);
		expect(mini).toMatch(/data-testid="leaderboard-metric-5-cost_per_success".*?data-state="not_applicable"/);
		expect(mini).toMatch(/data-testid="leaderboard-metric-5-tool_calls".*?data-state="unknown"/);
	});

	it("shows resilience as signed points, coloured, with unknown hatched and not run apart", () => {
		const html = render();
		expect(html).toMatch(/data-testid="leaderboard-resilience-1-stale-notes" data-tone="bad">-25 pts</);
		expect(html).toMatch(/data-testid="leaderboard-resilience-4-test-crash" data-tone="good">\+12\.5 pts</);
		expect(html).toMatch(/data-testid="leaderboard-resilience-4-stale-notes" data-tone="neutral">0 pts</);
		expect(html).toMatch(/data-state="unknown"[^>]*data-testid="leaderboard-resilience-2-test-crash"/);
		// Not run is a quiet mark whose accessible text still says "not run", never a number.
		expect(html).toMatch(
			/data-testid="leaderboard-resilience-3-test-crash"><span aria-hidden="true"[^>]*><\/span><span class="sr-only">not run</,
		);
		expect(html).toContain(">model-outage vs normal<");
	});

	it("leaves out resilience columns off the clean board", () => {
		const html = render(DEFAULT_SORT, []);
		expect(html).not.toContain("vs normal");
		expect(html).not.toContain("leaderboard-resilience-");
	});

	it("marks tier groups in rank order, including a tier 1 entry ranked below tier 2", () => {
		const html = render();
		const groups = [...html.matchAll(/data-testid="leaderboard-tier-group-(\d+)" data-tier="(\d)"/g)].map((m) => [
			Number(m[1]),
			Number(m[2]),
		]);
		expect(groups).toEqual([
			[0, 1],
			[3, 2],
			[4, 1],
		]);
		expect(html).toContain("Behind: clearly worse than the leader");
		expect(html).toContain('colSpan="16"');
	});

	it("drops the tier groups when sorted by another column, keeping the rank in each row", () => {
		const html = render({ key: "success", dir: "asc" });
		expect(html).not.toContain("leaderboard-tier-group-");
		const order = [...html.matchAll(/data-testid="leaderboard-row-(\d)"/g)].map((m) => Number(m[1]));
		expect(order).toEqual([5, 4, 3, 2, 1]);
	});

	it("sets aria-sort on the sorted column only and makes every sortable header a button", () => {
		const html = render({ key: "tokens", dir: "asc" });
		expect(html).toMatch(/aria-sort="ascending"[^>]*data-testid="leaderboard-head-tokens"/);
		expect(html).toMatch(/aria-sort="none"[^>]*data-testid="leaderboard-head-rank"/);
		expect(html.match(/aria-sort="(ascending|descending)"/g)).toHaveLength(1);
		expect(html).toMatch(/<button type="button"[^>]*aria-label="Sort by Tokens, sorted ascending"/);
		const desc = render({ key: "rank", dir: "desc" });
		expect(desc).toMatch(/aria-sort="descending"[^>]*data-testid="leaderboard-head-rank"/);
	});
	it("reverses the rows on a second click of the same header (By benchmark sort is wired)", () => {
		const ranks = (html: string) => [...html.matchAll(/data-testid="leaderboard-row-(\d+)"/g)].map((m) => Number(m[1]));
		const once = nextSort(DEFAULT_SORT, "tasks");
		const twice = nextSort(once, "tasks");
		expect(twice.key).toBe("tasks");
		expect(twice.dir).not.toBe(once.dir);
		const a = ranks(render(once));
		const b = ranks(render(twice));
		expect(a.length).toBe(board.entries.length);
		const tasks = (order: number[]) => order.map((r) => board.entries.find((e) => e.rank === r)?.tasks ?? 0);
		expect(tasks(a)).toEqual([...tasks(a)].sort((x, y) => (once.dir === "asc" ? x - y : y - x)));
		expect(tasks(b)).toEqual([...tasks(b)].sort((x, y) => (twice.dir === "asc" ? x - y : y - x)));
		expect(render(once)).toContain('data-testid="leaderboard-sort-tasks"');
	});
});
