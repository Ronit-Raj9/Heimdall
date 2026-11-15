import type { Tone } from "@/components/suites/suites.utils";
import type { EvidenceStrength, ObserverStatus, StepExecution, StepRecord, StepSource } from "@/lib/types/bench";

/** Every step source in display order: trusted witnesses first, the agent's own stream last. */
export const STEP_SOURCES: readonly StepSource[] = [
	"gateway",
	"tool_proxy",
	"process_observer",
	"workspace_observer",
	"network_observer",
	"shim",
	"agent_stream",
];

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

/** The agent's own stream is agent-reported; every other source is a witness outside the agent. */
export function sourceTone(source: string): Tone {
	return source === "agent_stream" ? "warn" : "neutral";
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

/** Rows that count toward the steps metric (Steps (model turns)). */
export function countedSteps(steps: readonly StepRecord[]): number {
	return steps.filter((step) => step.counted).length;
}

/** Characters that could reorder or hide displayed text (controls, bidi overrides), shown as U+FFFD. */
const UNSAFE_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F‪-‮⁦-⁩]/g;

/**
 * Agent-influenced text made safe to show on one line: control and bidi characters replaced, line
 * breaks folded to spaces, and clipped to max characters with an ellipsis. The result is still
 * rendered only as a React text node.
 */
export function clipText(text: string | undefined, max = MAX_TEXT): string {
	if (!text) return "";
	const flat = text.replace(UNSAFE_CHARS, "�").replace(/[\r\n\t]+/g, " ");
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