import { formatValueNumber } from "@/components/valueCell.utils";
import { formatSignedPp, shortHash, type Tone } from "@/components/verdict/verdict.utils";
import i18n from "@/lib/i18n";
import type { Leaderboard, LeaderboardEntry, Value, ValueKind } from "@/lib/types/bench";

// Pure helpers for the leaderboard screen: column definitions, client-side sorting, tier groups,
// the success bar geometry and the markdown export. Numbers follow the UI locale; callers pass
// getLocale(). Unknown and not-applicable values are never turned into numbers here.

export const LEADERBOARD_SCOPES = ["all", "common"] as const;
export const LEADERBOARD_SYNTHETIC = ["exclude", "only"] as const;
export const SORT_DIRS = ["asc", "desc"] as const;
export type SortDir = (typeof SORT_DIRS)[number];

/** The per-run aggregates shown as columns, in order, with their renderer. All are lower-is-better. */
export const METRIC_COLUMNS = [
	{ id: "cost_per_success", kind: "usd" },
	{ id: "steps", kind: "count" },
	{ id: "tool_calls", kind: "count" },
	{ id: "tokens", kind: "tokens" },
	{ id: "wall_time_s", kind: "duration" },
	{ id: "false_completion", kind: "rate" },
] as const satisfies readonly { id: string; kind: ValueKind }[];

export type MetricColumnId = (typeof METRIC_COLUMNS)[number]["id"];

/** Prefix of a resilience sort key: "resilience:model-outage". */
export const RESILIENCE_PREFIX = "resilience:";

export type SortKey = "rank" | "agent" | "success" | "reliability" | "tasks" | MetricColumnId | `${typeof RESILIENCE_PREFIX}${string}`;

export interface LeaderboardSort {
	key: SortKey;
	dir: SortDir;
}

/** The board's own order. */
export const DEFAULT_SORT: LeaderboardSort = { key: "rank", dir: "asc" };

const FIXED_SORT_KEYS: readonly string[] = ["rank", "agent", "success", "reliability", "tasks", ...METRIC_COLUMNS.map((c) => c.id)];

/**
 * The sort named in the URL, or the board's rank order when the key is not a column on this board
 * (for example a resilience column after switching to a fault condition).
 */
export function parseSort(key: string | null, dir: string | null, faults: readonly string[]): LeaderboardSort {
	if (!key) return DEFAULT_SORT;
	const known =
		FIXED_SORT_KEYS.includes(key) || (key.startsWith(RESILIENCE_PREFIX) && faults.includes(key.slice(RESILIENCE_PREFIX.length)));
	if (!known) return DEFAULT_SORT;
	const sortKey = key as SortKey;
	return { key: sortKey, dir: dir === "asc" || dir === "desc" ? dir : defaultDir(sortKey) };
}

/** First-click direction: best first. Rank and agent ascend, rates of success descend, costs ascend. */
export function defaultDir(key: SortKey): SortDir {
	if (key === "success" || key === "reliability" || key === "tasks" || key.startsWith(RESILIENCE_PREFIX)) return "desc";
	return "asc";
}

/** A click on a column header: the same column flips direction, a new column starts best first. */
export function nextSort(current: LeaderboardSort, key: SortKey): LeaderboardSort {
	if (current.key === key) return { key, dir: current.dir === "asc" ? "desc" : "asc" };
	return { key, dir: defaultDir(key) };
}

/** The number of an observed value, else null. Unknown and not applicable are never 0. */
export function observedNumber(value: Value | null | undefined): number | null {
	if (!value || value.state !== "observed" || typeof value.v !== "number" || !Number.isFinite(value.v)) return null;
	return value.v;
}

/** What a column sorts on for one entry; null sorts last in either direction. */
export function sortValue(entry: LeaderboardEntry, key: SortKey): number | string | null {
	switch (key) {
		case "rank":
			return entry.rank;
		case "agent":
			return `${entry.agent} ${entry.model} ${entry.label}`;
		case "success":
			return observedNumber(entry.success);
		case "reliability":
			return observedNumber(entry.reliability);
		case "tasks":
			return entry.tasks;
		default:
			if (key.startsWith(RESILIENCE_PREFIX)) return observedNumber(entry.resilience?.[key.slice(RESILIENCE_PREFIX.length)]);
			return observedNumber(entry.metrics?.[key]);
	}
}

/**
 * A sorted copy. Entries without a value for the column go last whatever the direction, so an
 * unknown never reads as the best or the worst. Ties keep the board's rank order.
 */
export function sortEntries(entries: readonly LeaderboardEntry[], sort: LeaderboardSort): LeaderboardEntry[] {
	const sign = sort.dir === "asc" ? 1 : -1;
	return [...entries].sort((a, b) => {
		const va = sortValue(a, sort.key);
		const vb = sortValue(b, sort.key);
		if (va === null || vb === null) {
			if (va !== vb) return va === null ? 1 : -1;
			return a.rank - b.rank;
		}
		const cmp = typeof va === "number" && typeof vb === "number" ? va - vb : String(va).localeCompare(String(vb));
		return cmp === 0 ? a.rank - b.rank : sign * cmp;
	});
}

/** 1 or 2. Anything but 1 is shown as behind the leader, never as tied with it. */
export function tierOf(entry: Pick<LeaderboardEntry, "tier">): 1 | 2 {
	return entry.tier === 1 ? 1 : 2;
}

/**
 * Row indexes that start a run of one tier, with that tier. In rank order tiers need not be
 * contiguous: an entry with few common tasks can rank below a tier 2 entry and still be tier 1.
 */
export function tierGroupStarts(rows: readonly LeaderboardEntry[]): Map<number, 1 | 2> {
	const starts = new Map<number, 1 | 2>();
	rows.forEach((row, i) => {
		const tier = tierOf(row);
		if (i === 0 || tierOf(rows[i - 1]) !== tier) starts.set(i, tier);
	});
	return starts;
}

/** Fault conditions with a resilience value on the board, in the board's condition order. */
export function resilienceConditions(board: Pick<Leaderboard, "conditions" | "entries">): string[] {
	const present = new Set<string>();
	for (const entry of board.entries ?? []) for (const condition of Object.keys(entry.resilience ?? {})) present.add(condition);
	const ordered = (board.conditions ?? []).filter((c) => present.has(c));
	const extra = [...present].filter((c) => !ordered.includes(c)).sort();
	return [...ordered, ...extra];
}

/** Bar geometry for an observed success: the fill and the interval whiskers, clamped to 0..1. */
export interface SuccessBar {
	fill: number;
	lo: number | null;
	hi: number | null;
}

export function successBar(value: Value | null | undefined): SuccessBar | null {
	const v = observedNumber(value);
	if (v === null) return null;
	const clamp = (x: number) => Math.min(1, Math.max(0, x));
	const bounds = value?.bounds;
	if (!bounds || !Number.isFinite(bounds[0]) || !Number.isFinite(bounds[1])) return { fill: clamp(v), lo: null, hi: null };
	return { fill: clamp(v), lo: clamp(Math.min(bounds[0], bounds[1])), hi: clamp(Math.max(bounds[0], bounds[1])) };
}

/** Resilience colour: lower under the fault is bad, higher is good, no change is neutral. */
export function resilienceTone(points: number): Tone {
	if (points < 0) return "bad";
	if (points > 0) return "good";
	return "neutral";
}

/** One markdown column: heading, right-aligned or not, and the cell text for an entry. */
interface MarkdownColumn {
	head: string;
	right: boolean;
	cell: (entry: LeaderboardEntry) => string;
}

/** Markdown of the rows as shown, in the shown order, with unknowns spelled out and never 0. */
export function leaderboardMarkdown(
	board: Pick<Leaderboard, "suite" | "condition" | "scope" | "synthetic" | "generated_at"> & { mode?: string },
	rows: readonly LeaderboardEntry[],
	faults: readonly string[],
	locale: string,
): string {
	const t = i18n.getFixedT(null, "bench");
	const value = (v: Value | null | undefined, kind: ValueKind) => {
		if (v?.state === "not_applicable") return t("leaderboard.markdown.notApplicable");
		const n = observedNumber(v);
		return n === null ? t("leaderboard.markdown.unknown") : formatValueNumber(n, kind, locale);
	};
	const interval = (entry: LeaderboardEntry) => {
		const bounds = observedNumber(entry.success) === null ? undefined : entry.success.bounds;
		return bounds ? `${formatValueNumber(bounds[0], "rate", locale)} - ${formatValueNumber(bounds[1], "rate", locale)}` : "";
	};
	const resilience = (entry: LeaderboardEntry, fault: string) => {
		const v = entry.resilience?.[fault];
		if (!v) return t("leaderboard.markdown.notRun");
		const n = observedNumber(v);
		return n === null ? value(v, "rate") : t("leaderboard.cell.points", { value: formatSignedPp(n, locale) });
	};
	const tier = (entry: LeaderboardEntry) =>
		t("leaderboard.markdown.tierValue", {
			tier: t(tierOf(entry) === 1 ? "leaderboard.tier.one" : "leaderboard.tier.two"),
			reason: entry.tier_reason,
		});

	const columns: MarkdownColumn[] = [
		{ head: t("leaderboard.table.rank"), right: true, cell: (e) => String(e.rank) },
		{ head: t("leaderboard.table.tier"), right: false, cell: tier },
		{ head: t("leaderboard.table.agent"), right: false, cell: (e) => e.agent },
		{ head: t("leaderboard.markdown.model"), right: false, cell: (e) => e.model },
		{ head: t("leaderboard.markdown.label"), right: false, cell: (e) => e.label },
		{ head: t("leaderboard.markdown.config"), right: false, cell: (e) => shortHash(e.config_hash) },
		{ head: t("leaderboard.table.success"), right: true, cell: (e) => value(e.success, "rate") },
		{ head: t("leaderboard.markdown.interval"), right: true, cell: interval },
		{ head: t("leaderboard.markdown.passes"), right: true, cell: (e) => `${e.passes.num}/${e.passes.den}` },
		{ head: t("leaderboard.table.reliability"), right: true, cell: (e) => value(e.reliability, "rate") },
		{ head: t("leaderboard.markdown.k"), right: true, cell: (e) => String(e.trials) },
		...METRIC_COLUMNS.map((c) => ({
			head: t(`leaderboard.table.${c.id}`),
			right: true,
			cell: (e: LeaderboardEntry) => value(e.metrics?.[c.id], c.kind),
		})),
		...faults.map((f) => ({
			head: `${f} ${t("leaderboard.table.resilienceVs")}`,
			right: true,
			cell: (e: LeaderboardEntry) => resilience(e, f),
		})),
		{ head: t("leaderboard.markdown.tasks"), right: true, cell: (e) => String(e.tasks) },
		{ head: t("leaderboard.markdown.runs"), right: true, cell: (e) => String(e.runs) },
	];

	const line = (cells: readonly string[]) => `| ${cells.map(escapeCell).join(" | ")} |`;
	const synthetic = board.synthetic === "only" || board.mode === "synthetic";
	return [
		`## ${t("leaderboard.markdown.title", { suite: board.suite, condition: board.condition })}`,
		"",
		...(synthetic ? [`> ${t("leaderboard.markdown.synthetic")}`, ""] : []),
		t("leaderboard.subtitle"),
		t("leaderboard.markdown.filters", { scope: board.scope, synthetic: board.synthetic, time: board.generated_at }),
		"",
		line(columns.map((c) => c.head)),
		`| ${columns.map((c) => (c.right ? "---:" : "---")).join(" | ")} |`,
		...rows.map((entry) => line(columns.map((c) => c.cell(entry)))),
		"",
	].join("\n");
}

// A table cell holds one line, and a pipe would end the cell early.
function escapeCell(text: string): string {
	return text.replace(/\\/g, "\\\\").replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
}