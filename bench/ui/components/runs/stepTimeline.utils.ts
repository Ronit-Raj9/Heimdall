import type { Tone } from "@/components/suites/suites.utils";
import type { EvidenceStrength, ObserverStatus, StepCorroboration, StepExecution, StepRecord, StepSource } from "@/lib/types/bench";

/**
 * Every step source in display order: trusted witnesses first, then the agent's own stream, and
 * last what the agent reported about itself (the weakest witness).
 */
export const STEP_SOURCES: readonly StepSource[] = [
	"gateway",
	"tool_proxy",
	"process_observer",
	"workspace_observer",
	"network_observer",
	"shim",
	"agent_stream",
	"agent_reported",
];

/** The source of rows the agent reported about itself (OTLP spans, the native event protocol, ...). */
export const REPORTED_SOURCE = "agent_reported";

/** Deepest nesting shown for agent-reported rows; deeper chains are drawn at this depth. */
export const MAX_DEPTH = 8;

/** Reported event types with a translated label. Any other value is shown as clipped raw text. */
export const REPORTED_EVENT_TYPES = [
	"plan",
	"thought_summary",
	"tool_call",
	"tool_result",
	"subagent_start",
	"subagent_end",
	"handoff",
	"memory_read",
	"memory_write",
	"retrieval",
	"message",
	"error",
	"custom",
] as const;
export type ReportedEventType = (typeof REPORTED_EVENT_TYPES)[number];

/** Report channels with a translated label. Any other value is shown as clipped raw text. */
export const REPORT_CHANNELS = ["otlp", "native", "file", "stdout"] as const;
export type KnownReportChannel = (typeof REPORT_CHANNELS)[number];

export const CORROBORATION_TONE: Record<StepCorroboration, Tone> = {
	corroborated: "good",
	agent_reported_only: "warn",
	conflicts: "bad",
};

/** The agent-chosen event type when it has a translated label, else undefined (show it as raw text). */
export function knownEventType(value: string | undefined): ReportedEventType | undefined {
	return (REPORTED_EVENT_TYPES as readonly string[]).includes(value ?? "") ? (value as ReportedEventType) : undefined;
}

/** The channel when it has a translated label, else undefined (show it as raw text). */
export function knownChannel(value: string | undefined): KnownReportChannel | undefined {
	return (REPORT_CHANNELS as readonly string[]).includes(value ?? "") ? (value as KnownReportChannel) : undefined;
}

/** The corroboration state when it is one the UI knows, else undefined. */
export function knownCorroboration(value: string | undefined): StepCorroboration | undefined {
	return value !== undefined && Object.hasOwn(CORROBORATION_TONE, value) ? (value as StepCorroboration) : undefined;
}

/**
 * Nesting depth of every row, by seq: how many reported ancestors its parent_seq chain reaches in
 * the timeline. A parent that is not in the timeline ends the chain; a cycle ends it at the first
 * repeat; depth stops at MAX_DEPTH. Rows without parent_seq have depth 0. Built from every row, so
 * hiding a source does not shift the indentation of the rows left visible.
 */
export function stepDepths(steps: readonly StepRecord[]): Map<number, number> {
	const parentOf = new Map<number, number | undefined>();
	for (const step of steps) parentOf.set(step.seq, step.parent_seq || undefined);
	const depths = new Map<number, number>();
	for (const step of steps) {
		const seen = new Set<number>([step.seq]);
		let depth = 0;
		let parent = parentOf.get(step.seq);
		while (parent !== undefined && depth < MAX_DEPTH && parentOf.has(parent) && !seen.has(parent)) {
			seen.add(parent);
			depth++;
			parent = parentOf.get(parent);
		}
		depths.set(step.seq, depth);
	}
	return depths;
}

/** Model rows lead their turn's group; everything else follows in time order. */
const MODEL_KINDS = new Set<string>(["model_turn", "model_attempt"]);

/** Longest agent-influenced string shown in a row; the rest is clipped. */
export const MAX_TEXT = 240;

export const PROOF_TONE: Record<EvidenceStrength, Tone> = {
	verified: "good",
	strong: "good",
	partial: "warn",
	inferred: "warn",
	unknown: "bad",
};

export const EXECUTION_TONE: Record<StepExecution, Tone> = {
	executed_with_proof: "good",
	executed_agent_reported: "warn",
	no_witness: "bad",
};

/**
 * The agent's own stream and the agent's reports about itself are agent-reported; every other
 * source is a witness outside the agent.
 */
export function sourceTone(source: string): Tone {
	return source === "agent_stream" || source === REPORTED_SOURCE ? "warn" : "neutral";
}

export const OBSERVER_TONE: Record<string, Tone> = { observed: "good", off: "neutral", missing: "bad" };

/** One logical model step: the model rows that answered it, then the rows it caused. */
export interface TurnGroup {
	key: string;
	turn: number;
	session?: number;
	model: StepRecord[];
	caused: StepRecord[];
}

/**
 * Rows grouped by session and turn, in the order each group first appears in the timeline. Within
 * a group the model rows come first, then commands, file changes and tool calls in time order.
 * Turn 0 holds what happened before the first model response.
 */
export function groupStepsByTurn(steps: readonly StepRecord[]): TurnGroup[] {
	const groups = new Map<string, TurnGroup>();
	for (const step of steps) {
		const key = `${step.session ?? 0}:${step.turn}`;
		let group = groups.get(key);
		if (!group) {
			group = { key, turn: step.turn, session: step.session, model: [], caused: [] };
			groups.set(key, group);
		}
		(MODEL_KINDS.has(step.kind) ? group.model : group.caused).push(step);
	}
	return [...groups.values()];
}

/** Rows whose source is not hidden. */
export function filterSteps(steps: readonly StepRecord[], hidden: ReadonlySet<string>): StepRecord[] {
	return hidden.size === 0 ? [...steps] : steps.filter((step) => !hidden.has(step.source));
}

/** Row count per source, for the filter chips. Sources outside STEP_SOURCES are counted too. */
export function sourceCounts(steps: readonly StepRecord[]): Map<string, number> {
	const counts = new Map<string, number>();
	for (const step of steps) counts.set(step.source, (counts.get(step.source) ?? 0) + 1);
	return counts;
}

/** The sources present in the timeline, known ones in STEP_SOURCES order, unknown ones after. */
export function presentSources(steps: readonly StepRecord[]): string[] {
	const counts = sourceCounts(steps);
	const known = STEP_SOURCES.filter((source) => counts.has(source));
	const unknown = [...counts.keys()].filter((source) => !(STEP_SOURCES as readonly string[]).includes(source)).sort();
	return [...known, ...unknown];
}

/** Rows that count toward agent_steps (Agent steps (all witnessed actions)), not toward steps. */
export function countedSteps(steps: readonly StepRecord[]): number {
	return steps.filter((step) => step.counted).length;
}

/** Characters that could reorder or hide displayed text (controls, bidi overrides), shown as U+FFFD. */
const UNSAFE_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F‪-‮⁦-⁩]/g;

/** Key-shaped strings: the per-run Bifrost virtual key (even only its start), other API keys, bearer tokens. */
const KEY_SHAPED =
	/(sk-bf-)[A-Za-z0-9_-]{2,}|\b(?:sk-[A-Za-z0-9_-]{12,}|sk_[A-Za-z0-9]{12,}|gh[pousr]_[A-Za-z0-9]{12,}|xox[abpr]-[A-Za-z0-9-]{8,}|AKIA[0-9A-Z]{12,})|(bearer\s+)(?!sk-bf-)[A-Za-z0-9._~+/=-]{8,}/gi;

/** Text with key-shaped strings masked; a known prefix is kept so a reader sees a key was there. */
export function maskKeys(text: string): string {
	return text.replace(KEY_SHAPED, (_m, bf?: string, bearer?: string) => (bf ? `${bf}[masked]` : bearer ? `${bearer}[masked]` : "[masked]"));
}

/**
 * Agent-influenced text made safe to show on one line: control and bidi characters replaced, line
 * breaks folded to spaces, and clipped to max characters with an ellipsis. The result is still
 * rendered only as a React text node.
 */
export function clipText(text: string | undefined, max = MAX_TEXT): string {
	if (!text) return "";
	const flat = maskKeys(text)
		.replace(UNSAFE_CHARS, "�")
		.replace(/[\r\n\t]+/g, " ");
	const chars = Array.from(flat);
	return chars.length <= max ? flat : `${chars.slice(0, max - 1).join("")}…`;
}

/** The time of day of a row, HH:MM:SS in UTC, or the raw value when it does not parse. */
export function stepTime(ts: string): string {
	const date = new Date(ts);
	if (Number.isNaN(date.getTime())) return clipText(ts, 32);
	return date.toISOString().slice(11, 19);
}

/** True when the observer could not see everything: missing, or it dropped records. */
export function observerDegraded(status: ObserverStatus): boolean {
	return status.state === "missing" || (status.dropped ?? 0) > 0;
}

/** A count from the agent events status as a finite, non-negative number; anything else reads as 0. */
export function safeCount(value: unknown): number {
	return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

/** Most per-channel or per-type entries shown in the agent events line; the rest are summed as "more". */
export const MAX_EVENT_ENTRIES = 8;

/**
 * Per-channel or per-type counts of the agent events status, largest first (known channels break
 * ties in REPORT_CHANNELS order, then keys sort), zero entries left out, at most MAX_EVENT_ENTRIES.
 * Keys are agent-influenced display text. rest is the number of entries left out.
 */
export function eventEntries(counts: Record<string, unknown> | undefined): { entries: [string, number][]; rest: number } {
	if (!counts || typeof counts !== "object") return { entries: [], rest: 0 };
	const order = (key: string) => {
		const i = (REPORT_CHANNELS as readonly string[]).indexOf(key);
		return i < 0 ? REPORT_CHANNELS.length : i;
	};
	const all = Object.entries(counts)
		.map(([key, n]): [string, number] => [key, safeCount(n)])
		.filter(([, n]) => n > 0)
		.sort(([a, x], [b, y]) => y - x || order(a) - order(b) || (a < b ? -1 : a > b ? 1 : 0));
	return { entries: all.slice(0, MAX_EVENT_ENTRIES), rest: Math.max(0, all.length - MAX_EVENT_ENTRIES) };
}