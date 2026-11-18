import type { Tone } from "@/components/suites/suites.utils";
import type {
	AgentAttributionStrength,
	AgentCoverage,
	AgentEdgeKind,
	AgentHandoff,
	AgentSpan,
	Blame,
	BlameCandidate,
	BlameReplayStatus,
	BlameTier,
	HandoffFact,
} from "@/lib/types/bench";

// Pure helpers of the run page's Agents section: tree order, swimlane layout, parallelism, the
// sent-versus-delivered line diff, coverage tone and formatters. Everything here is testable
// without a browser. Strings that came from an agent pass through untouched apart from the
// line sanitising in payloadLines; callers render them as React text nodes only.

/** Attribution strengths, strongest first. */
export const STRENGTHS: readonly AgentAttributionStrength[] = ["proven", "declared", "reported"];

/** Attribution sources, strongest first (schemas.AgentSources). */
export const SOURCES = ["gateway_key", "header", "transcript", "otlp", "self_report"] as const;
export type KnownSource = (typeof SOURCES)[number];

/** Edge kinds with a translated label. */
export const EDGE_KINDS: readonly AgentEdgeKind[] = ["spawn", "handoff", "message", "claim", "return"];

/** Blame tiers, strongest evidence first; single_agent sits apart (nothing to attribute). */
export const BLAME_TIERS: readonly BlameTier[] = ["replay_validated", "gate_proven", "unattributed", "single_agent"];

export const REPLAY_STATUSES: readonly BlameReplayStatus[] = ["not_run", "flipped", "held", "error"];

/** Exit reasons with a translated label; "exit code n" and anything else is shown as text. */
export const EXITS = ["completed", "error", "timeout"] as const;
export type KnownExit = (typeof EXITS)[number];

/** Gate effects with a translated label (schemas.GateEffect). */
export const GATE_EFFECTS = ["invalidate", "veto", "report", "admission"] as const;
export type KnownGateEffect = (typeof GATE_EFFECTS)[number];

/** A credential the bench minted is proof; a header the agent chose is a declaration; the agent's own record is a report. */
export const STRENGTH_TONE: Record<AgentAttributionStrength, Tone> = { proven: "good", declared: "neutral", reported: "warn" };

/** Tone of each tier: a validated or proven blame names an agent; unattributed is the honest "could not prove it". */
export const TIER_TONE: Record<BlameTier, Tone> = {
	replay_validated: "good",
	gate_proven: "good",
	unattributed: "warn",
	single_agent: "neutral",
};

/** flipped validates the blame; held says the suspect alone does not explain the failure. */
export const REPLAY_TONE: Record<BlameReplayStatus, Tone> = { not_run: "neutral", flipped: "good", held: "warn", error: "bad" };

export const EXIT_TONE: Record<KnownExit, Tone> = { completed: "good", error: "bad", timeout: "warn" };

export const EFFECT_TONE: Record<KnownGateEffect, Tone> = { invalidate: "bad", veto: "bad", report: "warn", admission: "neutral" };

function known<T extends string>(list: readonly T[], value: string | undefined): T | undefined {
	return value !== undefined && (list as readonly string[]).includes(value) ? (value as T) : undefined;
}

export const knownStrength = (value: string | undefined) => known(STRENGTHS, value);
export const knownSource = (value: string | undefined) => known(SOURCES, value);
export const knownEdgeKind = (value: string | undefined) => known(EDGE_KINDS, value);
export const knownTier = (value: string | undefined) => known(BLAME_TIERS, value);
export const knownReplayStatus = (value: string | undefined) => known(REPLAY_STATUSES, value);
export const knownExit = (value: string | undefined) => known(EXITS, value);
export const knownEffect = (value: string | undefined) => known(GATE_EFFECTS, value);

/** The strength of an attribution source (schemas.AgentSourceStrength): an unknown source is reported. */
export function sourceStrength(source: string | undefined): AgentAttributionStrength {
	if (source === "gateway_key") return "proven";
	if (source === "header") return "declared";
	return "reported";
}

/** The tone of a strength; an unknown one is treated as the weakest, reported. */
export function strengthTone(value: string | undefined): Tone {
	const strength = knownStrength(value);
	return strength ? STRENGTH_TONE[strength] : "warn";
}

/** The tone of an exit: the known reasons, "exit code 0" as completed, any other code as an error. */
export function exitTone(value: string | undefined): Tone {
	const exit = knownExit(value);
	if (exit) return EXIT_TONE[exit];
	const code = /^exit code (-?\d+)$/.exec(value ?? "");
	if (code) return code[1] === "0" ? "good" : "bad";
	return "neutral";
}

/** Deepest nesting drawn in the tree; deeper chains are drawn at this depth. */
export const MAX_TREE_DEPTH = 8;

/** One node in tree order with the depth it is drawn at. */
export interface TreeEntry<T> {
	node: T;
	depth: number;
	/** The node's parent is not in the list: drawn as a root. */
	orphan: boolean;
}

/**
 * Nodes depth first by parent id: each root (no parent, or a parent not in the list) then its
 * children in input order. A parent cycle never loops: a node is placed once, and nodes only
 * reachable through a cycle are placed as roots at the end. Depth comes from the chain, capped at
 * MAX_TREE_DEPTH, not from the server's depth field.
 */
export function treeOrder<T>(nodes: readonly T[], idOf: (node: T) => string, parentOf: (node: T) => string | undefined): TreeEntry<T>[] {
	const ids = new Set(nodes.map(idOf));
	const children = new Map<string, T[]>();
	const roots: T[] = [];
	for (const node of nodes) {
		const parent = parentOf(node);
		if (parent && parent !== idOf(node) && ids.has(parent)) {
			const list = children.get(parent) ?? [];
			list.push(node);
			children.set(parent, list);
		} else {
			roots.push(node);
		}
	}
	const out: TreeEntry<T>[] = [];
	const placed = new Set<T>();
	const visit = (node: T, depth: number, orphan: boolean) => {
		if (placed.has(node)) return;
		placed.add(node);
		out.push({ node, depth: Math.min(depth, MAX_TREE_DEPTH), orphan });
		for (const child of children.get(idOf(node)) ?? []) visit(child, depth + 1, false);
	};
	// A root with a parent id names a missing agent or itself: drawn as a root, marked orphan.
	for (const root of roots) visit(root, 0, Boolean(parentOf(root)));
	// Whatever is left hangs off a cycle: place each as a root so nothing disappears.
	for (const node of nodes) visit(node, 0, true);
	return out;
}

/** One block of a lane, placed as percentages of the run's wall time. */
export interface LaneBlock {
	span: AgentSpan;
	left: number;
	width: number;
}

export interface Lane {
	agentId: string;
	blocks: LaneBlock[];
	/** The agent has a block on the critical path. */
	critical: boolean;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/**
 * One lane per agent in the given order, then a lane for every agent that only appears in the
 * spans (first seen first). Blocks are sorted by start and placed over wallMs as percentages,
 * clamped to the run; a zero or unknown wall time draws every block at the start with no width
 * (the stylesheet keeps a minimum visible width).
 */
export function laneLayout(spans: readonly AgentSpan[], agentOrder: readonly string[], wallMs: number): Lane[] {
	const order = [...new Set(agentOrder)];
	for (const span of spans) if (!order.includes(span.agent_id)) order.push(span.agent_id);
	const wall = Number.isFinite(wallMs) && wallMs > 0 ? wallMs : 0;
	const lanes = new Map<string, Lane>(order.map((id) => [id, { agentId: id, blocks: [], critical: false }]));
	const sorted = [...spans].sort((a, b) => a.start_ms - b.start_ms || a.seq - b.seq);
	for (const span of sorted) {
		const lane = lanes.get(span.agent_id)!;
		const start = wall ? clamp(span.start_ms, 0, wall) : 0;
		const end = wall ? clamp(Math.max(span.end_ms, span.start_ms), start, wall) : 0;
		lane.blocks.push({ span, left: wall ? (start / wall) * 100 : 0, width: wall ? ((end - start) / wall) * 100 : 0 });
		if (span.critical) lane.critical = true;
	}
	return order.map((id) => lanes.get(id)!);
}

/** One handoff drawn on the swimlanes: at its time, from the sender's lane to the receiver's. */
export interface HandoffMarker {
	handoff: AgentHandoff;
	/** Percent of the wall time. */
	left: number;
	fromLane: number;
	toLane: number;
}

/**
 * The handoffs that can be drawn on the lanes: a known time (at_ms) and both ends a lane. Others
 * (no time, no recipient, an agent with no lane) stay in the handoff list only. Placed like blocks:
 * a percent of wallMs, clamped to the run; a zero wall time puts every marker at the start.
 */
export function handoffMarkers(handoffs: readonly AgentHandoff[], laneOrder: readonly string[], wallMs: number): HandoffMarker[] {
	const lane = new Map(laneOrder.map((id, i) => [id, i]));
	const wall = Number.isFinite(wallMs) && wallMs > 0 ? wallMs : 0;
	const out: HandoffMarker[] = [];
	for (const handoff of handoffs) {
		if (handoff.at_ms === null || handoff.at_ms === undefined || !Number.isFinite(handoff.at_ms) || !handoff.to) continue;
		const fromLane = lane.get(handoff.from);
		const toLane = lane.get(handoff.to);
		if (fromLane === undefined || toLane === undefined) continue;
		out.push({ handoff, left: wall ? (clamp(handoff.at_ms, 0, wall) / wall) * 100 : 0, fromLane, toLane });
	}
	return out;
}

/** busy / wall, or null when the wall time is zero or unknown. */
export function parallelism(busyMs: number, wallMs: number): number | null {
	if (!Number.isFinite(busyMs) || !Number.isFinite(wallMs) || wallMs <= 0 || busyMs < 0) return null;
	return busyMs / wallMs;
}

/** A share of the wall time in percent, or null when the wall time is zero. */
export function shareOfWall(ms: number, wallMs: number): number | null {
	if (!Number.isFinite(ms) || !Number.isFinite(wallMs) || wallMs <= 0) return null;
	return (ms / wallMs) * 100;
}

/** Kinds of the timeline rows with a colour of their own; any other kind takes the "other" colour. */
export const SPAN_KINDS = [
	"model_turn",
	"model_attempt",
	"tool_request",
	"tool_execution",
	"command",
	"file_change",
	"network",
	"agent_event",
] as const;
export type KnownSpanKind = (typeof SPAN_KINDS)[number];

const KIND_COLOR: Record<KnownSpanKind, string> = {
	model_turn: "var(--chart-cat-1)",
	model_attempt: "var(--chart-cat-5)",
	tool_request: "var(--chart-cat-2)",
	tool_execution: "var(--chart-cat-3)",
	command: "var(--chart-cat-4)",
	file_change: "var(--chart-cat-6)",
	network: "var(--chart-info)",
	agent_event: "var(--chart-cat-other)",
};

export const knownSpanKind = (value: string | undefined) => known(SPAN_KINDS, value);

/** The CSS colour of a block's kind. */
export function kindColor(kind: string): string {
	const k = knownSpanKind(kind);
	return k ? KIND_COLOR[k] : "var(--chart-cat-other)";
}

/** The kinds present in the spans, in SPAN_KINDS order, unknown kinds last in first-seen order. */
export function presentKinds(spans: readonly AgentSpan[]): string[] {
	const seen = new Set(spans.map((span) => span.kind));
	const knownKinds = SPAN_KINDS.filter((kind) => seen.has(kind));
	const other = [...seen].filter((kind) => !knownSpanKind(kind));
	return [...knownKinds, ...other];
}

/** Coverage at or above this percent is good, at or above WARN a warning, else bad. */
export const COVERAGE_GOOD = 95;
export const COVERAGE_WARN = 60;

export function coverageTone(percent: number | null): Tone {
	if (percent === null) return "neutral";
	if (percent >= COVERAGE_GOOD) return "good";
	if (percent >= COVERAGE_WARN) return "warn";
	return "bad";
}

/** attributed / steps in percent, or null when no step was considered. */
export function coveragePercent(coverage: Pick<AgentCoverage, "steps" | "attributed">): number | null {
	if (!(coverage.steps > 0) || !Number.isFinite(coverage.attributed)) return null;
	return clamp((coverage.attributed / coverage.steps) * 100, 0, 100);
}

/** Counts ordered by a reference list (strongest first), keys not in it after, then by name. */
export function orderedCounts(counts: Record<string, number> | undefined, order: readonly string[]): [string, number][] {
	const rank = (key: string) => {
		const i = order.indexOf(key);
		return i < 0 ? order.length : i;
	};
	return Object.entries(counts ?? {})
		.filter(([, n]) => typeof n === "number" && Number.isFinite(n) && n > 0)
		.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b));
}

/** What became of one planted fact on a handoff. */
export type FactState = "kept" | "lost" | "added" | "absent";

export function factState(fact: HandoffFact): FactState {
	if (fact.sent) return fact.delivered ? "kept" : "lost";
	return fact.delivered ? "added" : "absent";
}

export const FACT_TONE: Record<FactState, Tone> = { kept: "good", lost: "warn", added: "warn", absent: "neutral" };

/** One line of a sent-versus-delivered diff: kept, only sent (removed) or only delivered (added). */
export interface DiffLine {
	op: "same" | "del" | "add";
	text: string;
}

/** Above this many cells the LCS table is skipped and the differing middle shows as removed then added. */
export const MAX_DIFF_CELLS = 250_000;

/**
 * A line diff of two texts by longest common subsequence, after trimming the common prefix and
 * suffix. Removed lines (only in a) come before added lines (only in b) at each change.
 */
export function lineDiff(a: string, b: string): DiffLine[] {
	const left = splitLines(a);
	const right = splitLines(b);
	let start = 0;
	while (start < left.length && start < right.length && left[start] === right[start]) start++;
	let endA = left.length;
	let endB = right.length;
	while (endA > start && endB > start && left[endA - 1] === right[endB - 1]) {
		endA--;
		endB--;
	}
	const head: DiffLine[] = left.slice(0, start).map((text) => ({ op: "same", text }));
	const tail: DiffLine[] = left.slice(endA).map((text) => ({ op: "same", text }));
	const midA = left.slice(start, endA);
	const midB = right.slice(start, endB);
	return [...head, ...diffMiddle(midA, midB), ...tail];
}

function diffMiddle(a: string[], b: string[]): DiffLine[] {
	const n = a.length;
	const m = b.length;
	if (n === 0 || m === 0 || n * m > MAX_DIFF_CELLS) {
		return [...a.map((text): DiffLine => ({ op: "del", text })), ...b.map((text): DiffLine => ({ op: "add", text }))];
	}
	// lcs[i][j] is the LCS length of a[i:] and b[j:], in one flat row-major array.
	const width = m + 1;
	const lcs = new Uint32Array((n + 1) * width);
	for (let i = n - 1; i >= 0; i--) {
		for (let j = m - 1; j >= 0; j--) {
			lcs[i * width + j] = a[i] === b[j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
		}
	}
	const out: DiffLine[] = [];
	let i = 0;
	let j = 0;
	while (i < n && j < m) {
		if (a[i] === b[j]) {
			out.push({ op: "same", text: a[i] });
			i++;
			j++;
		} else if (lcs[(i + 1) * width + j] >= lcs[i * width + j + 1]) {
			out.push({ op: "del", text: a[i++] });
		} else {
			out.push({ op: "add", text: b[j++] });
		}
	}
	while (i < n) out.push({ op: "del", text: a[i++] });
	while (j < m) out.push({ op: "add", text: b[j++] });
	return out;
}

function splitLines(text: string): string[] {
	if (text.length === 0) return [];
	return text.replace(/\r\n?/g, "\n").replace(/\n$/, "").split("\n");
}

/** Characters that could reorder or hide displayed text (controls, bidi overrides), shown as U+FFFD. Tabs and line breaks stay. */
const UNSAFE_PAYLOAD_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F‪-‮⁦-⁩]/g;

/** Agent payload text made safe to show as lines: control and bidi characters replaced, nothing else changed. */
export function safePayload(text: string | undefined): string {
	return (text ?? "").replace(UNSAFE_PAYLOAD_CHARS, "�");
}

/** Whether a candidate is the one the blame picked: same agent, gate and step. */
export function isBlamedCandidate(candidate: BlameCandidate, blame: Pick<Blame, "agent_id" | "gate" | "step_seq">): boolean {
	return (
		Boolean(blame.agent_id) &&
		candidate.agent_id === blame.agent_id &&
		candidate.gate === blame.gate &&
		(candidate.step_seq ?? 0) === (blame.step_seq ?? 0)
	);
}

/** A tier that names an agent. */
export function tierNamesAgent(tier: string | undefined): boolean {
	return tier === "replay_validated" || tier === "gate_proven";
}

/** "$0.0123": USD with four decimals, since per-agent costs are often fractions of a cent. */
export function formatUsd4(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { style: "currency", currency: "USD", minimumFractionDigits: 4, maximumFractionDigits: 4 }).format(
		value,
	);
}

/** A duration in milliseconds as "850 ms", "1.2 sec" or "3 min 4 sec" for the UI locale. */
export function formatDurationMs(ms: number, locale: string): string {
	const unit = (value: number, u: "millisecond" | "second" | "minute", digits = 0) =>
		new Intl.NumberFormat(locale, { style: "unit", unit: u, unitDisplay: "short", maximumFractionDigits: digits }).format(value);
	if (!Number.isFinite(ms)) return "";
	if (ms < 0) return `-${formatDurationMs(-ms, locale)}`;
	if (ms < 1000) return unit(Math.round(ms), "millisecond");
	// Round once, then split, so 179.6 s reads "3 min", never "2 min 60 sec".
	const tenths = Math.round(ms / 100);
	if (tenths < 600) return unit(tenths / 10, "second", 1);
	const total = Math.round(ms / 1000);
	const minutes = Math.floor(total / 60);
	const seconds = total % 60;
	return seconds === 0 ? unit(minutes, "minute") : `${unit(minutes, "minute")} ${unit(seconds, "second")}`;
}

/** A byte count as "512 B", "1.4 kB" or "2.1 MB" for the UI locale. */
export function formatBytes(bytes: number, locale: string): string {
	const unit = (value: number, u: "kilobyte" | "megabyte") =>
		new Intl.NumberFormat(locale, { style: "unit", unit: u, unitDisplay: "short", maximumFractionDigits: 1 }).format(value);
	if (!Number.isFinite(bytes)) return "";
	if (bytes < 1000) return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(bytes)} B`;
	if (bytes < 1_000_000) return unit(bytes / 1000, "kilobyte");
	return unit(bytes / 1_000_000, "megabyte");
}

/** A number with one decimal for the UI locale. */
export function formatOneDecimal(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(value);
}

/** A whole percent for the UI locale ("97%"); values under one but above zero show one decimal. */
export function formatPercent(percent: number, locale: string): string {
	const digits = percent > 0 && percent < 1 ? 1 : 0;
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: digits }).format(percent / 100);
}

/** The time of day of an ISO time, HH:MM:SS.mmm in UTC, or null when absent or unparsable. */
export function timeOfDay(iso: string | undefined): string | null {
	if (!iso) return null;
	const date = new Date(iso);
	if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return null;
	return date.toISOString().slice(11, 23);
}