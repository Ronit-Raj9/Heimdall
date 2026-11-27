import "@/lib/i18n";

import type { LeaderboardEntry, LeaderboardResponse, Value } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	DEFAULT_SORT,
	defaultDir,
	leaderboardMarkdown,
	nextSort,
	observedNumber,
	parseSort,
	resilienceConditions,
	resilienceTone,
	sortEntries,
	successBar,
	tierGroupStarts,
	tierOf,
} from "./leaderboard.utils";

const board = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/leaderboard.json"), "utf8")) as LeaderboardResponse;
const FAULTS = ["model-outage", "stale-notes", "test-crash"];

const obs = (v: number, bounds?: [number, number]): Value => ({ state: "observed", v, evidence: "outcome", ...(bounds ? { bounds } : {}) });
const unknown: Value = { state: "unknown", v: null, reason: "telemetry_incomplete" };
const na: Value = { state: "not_applicable", v: null };

function entry(rank: number, overrides: Partial<LeaderboardEntry> = {}): LeaderboardEntry {
	return {
		rank,
		tier: 1,
		p_vs_leader: rank === 1 ? null : 0.5,
		tier_reason: rank === 1 ? "leader" : "p 0.5 on 8 common tasks",
		config_hash: `sha256:${String(rank).repeat(64)}`,
		agent: `agent-${rank}`,
		model: "m",
		label: "",
		synthetic: false,
		experiments: [],
		tasks: 8,
		runs: 24,
		trials: 3,
		success: obs(1 - rank / 10),
		passes: { num: 1, den: 2, unknown: 0 },
		reliability: obs(0.5),
		metrics: {},
		resilience: {},
		...overrides,
	};
}

const ranks = (rows: LeaderboardEntry[]) => rows.map((r) => r.rank);

describe("parseSort", () => {
	const cases = [
		{ name: "absent is rank order", key: null, dir: null, want: DEFAULT_SORT },
		{ name: "unknown column falls back to rank", key: "blended_score", dir: "desc", want: DEFAULT_SORT },
		{ name: "known column, given direction", key: "steps", dir: "desc", want: { key: "steps", dir: "desc" } },
		{ name: "known column, bad direction takes its default", key: "success", dir: "sideways", want: { key: "success", dir: "desc" } },
		{
			name: "resilience column on this board",
			key: "resilience:stale-notes",
			dir: null,
			want: { key: "resilience:stale-notes", dir: "desc" },
		},
		{ name: "resilience column not on this board", key: "resilience:empty-test-run", dir: "asc", want: DEFAULT_SORT },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(parseSort(c.key, c.dir, FAULTS)).toEqual(c.want);
		});
	}
});

describe("defaultDir and nextSort", () => {
	it("starts best first", () => {
		expect(defaultDir("rank")).toBe("asc");
		expect(defaultDir("agent")).toBe("asc");
		expect(defaultDir("success")).toBe("desc");
		expect(defaultDir("reliability")).toBe("desc");
		expect(defaultDir("resilience:model-outage")).toBe("desc");
		expect(defaultDir("cost_per_success")).toBe("asc");
		expect(defaultDir("false_completion")).toBe("asc");
	});

	it("flips the same column and starts a new one at its default", () => {
		expect(nextSort(DEFAULT_SORT, "rank")).toEqual({ key: "rank", dir: "desc" });
		expect(nextSort({ key: "rank", dir: "desc" }, "rank")).toEqual({ key: "rank", dir: "asc" });
		expect(nextSort(DEFAULT_SORT, "tokens")).toEqual({ key: "tokens", dir: "asc" });
		expect(nextSort({ key: "tokens", dir: "asc" }, "success")).toEqual({ key: "success", dir: "desc" });
	});
});

describe("observedNumber", () => {
	it("is a number only when observed", () => {
		expect(observedNumber(obs(0))).toBe(0);
		expect(observedNumber(unknown)).toBeNull();
		expect(observedNumber(na)).toBeNull();
		expect(observedNumber(undefined)).toBeNull();
		expect(observedNumber({ state: "observed", v: null })).toBeNull();
	});
});

describe("sortEntries", () => {
	const rows = [
		entry(1, { metrics: { steps: obs(30) } }),
		entry(2, { metrics: { steps: unknown } }),
		entry(3, { metrics: { steps: obs(10) } }),
		entry(4, { metrics: {} }),
		entry(5, { metrics: { steps: obs(10) } }),
	];

	it("keeps rank order by default and reverses it on demand", () => {
		expect(ranks(sortEntries([...rows].reverse(), DEFAULT_SORT))).toEqual([1, 2, 3, 4, 5]);
		expect(ranks(sortEntries(rows, { key: "rank", dir: "desc" }))).toEqual([5, 4, 3, 2, 1]);
	});

	it("puts unknown and missing values last in both directions, never as 0", () => {
		expect(ranks(sortEntries(rows, { key: "steps", dir: "asc" }))).toEqual([3, 5, 1, 2, 4]);
		expect(ranks(sortEntries(rows, { key: "steps", dir: "desc" }))).toEqual([1, 3, 5, 2, 4]);
	});

	it("sorts not applicable last too", () => {
		const cps = [entry(1, { metrics: { cost_per_success: na } }), entry(2, { metrics: { cost_per_success: obs(0.5) } })];
		expect(ranks(sortEntries(cps, { key: "cost_per_success", dir: "asc" }))).toEqual([2, 1]);
	});

	it("sorts a resilience column and the agent name", () => {
		const res = [
			entry(1, { resilience: { "model-outage": obs(-0.25) } }),
			entry(2, { resilience: {} }),
			entry(3, { resilience: { "model-outage": obs(0.1) } }),
		];
		expect(ranks(sortEntries(res, { key: "resilience:model-outage", dir: "desc" }))).toEqual([3, 1, 2]);
		const named = [entry(1, { agent: "zeta" }), entry(2, { agent: "alpha" })];
		expect(ranks(sortEntries(named, { key: "agent", dir: "asc" }))).toEqual([2, 1]);
	});

	it("does not mutate its input", () => {
		const input = [entry(2), entry(1)];
		sortEntries(input, DEFAULT_SORT);
		expect(ranks(input)).toEqual([2, 1]);
	});
});

describe("tiers", () => {
	it("treats anything but 1 as behind the leader", () => {
		expect(tierOf({ tier: 1 })).toBe(1);
		expect(tierOf({ tier: 2 })).toBe(2);
		expect(tierOf({ tier: 0 })).toBe(2);
	});

	it("marks the start of each tier run, including a tier 1 entry ranked below tier 2", () => {
		const starts = tierGroupStarts([entry(1), entry(2), entry(3, { tier: 2 }), entry(4, { tier: 1 })]);
		expect([...starts.entries()]).toEqual([
			[0, 1],
			[2, 2],
			[3, 1],
		]);
		expect(tierGroupStarts([]).size).toBe(0);
	});
});

describe("resilienceConditions", () => {
	it("follows the board's condition order and adds unlisted ones sorted", () => {
		const entries = [
			entry(1, { resilience: { "test-crash": obs(0), zeta: obs(0) } }),
			entry(2, { resilience: { "model-outage": obs(0) } }),
		];
		expect(resilienceConditions({ conditions: ["clean", "model-outage", "stale-notes", "test-crash"], entries })).toEqual([
			"model-outage",
			"test-crash",
			"zeta",
		]);
	});

	it("reads the fixture's fault columns", () => {
		expect(resilienceConditions(board)).toEqual(FAULTS);
	});
});

describe("successBar", () => {
	it("draws the fill and the interval, clamped to 0..1", () => {
		expect(successBar(obs(0.5, [0.25, 0.75]))).toEqual({ fill: 0.5, lo: 0.25, hi: 0.75 });
		expect(successBar(obs(0.2, [-0.1, 1.2]))).toEqual({ fill: 0.2, lo: 0, hi: 1 });
	});

	it("draws no whiskers without bounds and nothing for unknown", () => {
		expect(successBar(obs(0.5))).toEqual({ fill: 0.5, lo: null, hi: null });
		expect(successBar(unknown)).toBeNull();
		expect(successBar({ ...unknown, bounds: [0, 1] })).toBeNull();
	});
});

describe("resilienceTone", () => {
	it("is red for lower, green for higher, neutral for no change", () => {
		expect(resilienceTone(-0.1)).toBe("bad");
		expect(resilienceTone(0.1)).toBe("good");
		expect(resilienceTone(0)).toBe("neutral");
	});
});

describe("leaderboardMarkdown", () => {
	const md = leaderboardMarkdown(board, board.entries, FAULTS, "en");
	const lines = md.split("\n");
	const tableLines = lines.filter((line) => line.startsWith("|"));

	it("is a table with a header, an alignment row and one line per entry", () => {
		expect(lines[0]).toBe("## Leaderboard: coding-core@1.0.0, clean");
		expect(tableLines).toHaveLength(2 + board.entries.length);
		const columns = tableLines[0].split(" | ").length;
		for (const line of tableLines) expect(line.split(/(?<!\\) \| /).length).toBe(columns);
		expect(tableLines[0]).toContain("| Rank | Group | Agent setup |");
		expect(tableLines[0]).toContain("model-outage vs normal");
		expect(tableLines[1]).toMatch(/^\| ---: \| --- \|/);
	});

	it("labels synthetic data and keeps the no-blended-score explainer", () => {
		expect(md).toContain("> PRACTICE DATA: no real model.");
		expect(md).toContain("Ranked by tasks solved.");
	});

	it("spells out unknown, n/a and not run instead of 0", () => {
		const mini = tableLines.find((line) => line.includes("mini-swe-agent"))!;
		expect(mini).toContain("| n/a |");
		expect(mini).toContain("| not run | not run | not run |");
		const custom = tableLines.find((line) => line.includes("custom-python"))!;
		expect(custom).toMatch(/\| 25% \| 0% - 57% \| 2\/8 \| unknown \| 1 \|/);
		expect(custom).toContain("Behind (p 0.012 on 8 common tasks)");
		const baseline = tableLines.find((line) => line.includes("A: baseline"))!;
		expect(baseline).toContain("| -20.8 pts | -4.2 pts | unknown |");
	});

	it("escapes pipes inside cells", () => {
		const out = leaderboardMarkdown({ ...board, mode: "live", synthetic: "exclude" }, [entry(1, { label: "a|b" })], [], "en");
		expect(out).toContain("a\\|b");
		expect(out).not.toContain("SYNTHETIC");
	});
});

describe("leaderboard fixture", () => {
	it("is synthetic and labelled so", () => {
		expect(board.mode).toBe("synthetic");
		expect(board.synthetic).toBe("only");
		for (const e of board.entries) expect(e.synthetic).toBe(true);
	});

	it("is ordered by rank with one leader and tiers 1 or 2", () => {
		expect(ranks(board.entries)).toEqual(board.entries.map((_, i) => i + 1));
		expect(board.entries.filter((e) => e.p_vs_leader === null)).toHaveLength(1);
		expect(board.entries[0]).toMatchObject({ tier: 1, p_vs_leader: null, tier_reason: "leader" });
		for (const e of board.entries) expect([1, 2]).toContain(e.tier);
		expect(new Set(board.entries.map((e) => e.config_hash)).size).toBe(board.entries.length);
	});

	it("keeps every value's number in step with its state", () => {
		for (const e of board.entries) {
			const values = [e.success, e.reliability, ...Object.values(e.metrics), ...Object.values(e.resilience)];
			for (const v of values) expect(v.state === "observed", JSON.stringify(v)).toBe(typeof v.v === "number");
			expect(e.passes.den).toBeGreaterThanOrEqual(e.passes.num);
		}
	});

	it("covers the screen's edge cases", () => {
		expect(board.entries.some((e) => e.reliability.state === "unknown")).toBe(true);
		expect(board.entries.some((e) => e.metrics.cost_per_success?.state === "not_applicable")).toBe(true);
		expect(board.entries.some((e) => Object.values(e.resilience).some((v) => v.state === "unknown"))).toBe(true);
		expect(board.entries.some((e) => e.tier === 2)).toBe(true);
		expect(board.excluded.length).toBeGreaterThan(0);
		expect(board.common_tasks.length).toBeGreaterThan(0);
	});
});