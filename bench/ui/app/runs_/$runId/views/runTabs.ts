// The run page's tabs and when each applies. Pure, so the rule is tested without a page.
import type { OutputKind, RunDetail } from "@/lib/types/bench";

export const RUN_TABS = ["overview", "timeline", "output", "agents", "visual", "voice", "gpu", "services", "security", "evidence"] as const;

export type RunTabId = (typeof RUN_TABS)[number];

/** What the run recorded that decides its tabs. */
export interface RunFacts {
	output: OutputKind;
	/** A multi-agent run (GET /runs/{id}/agents answers available true). */
	agents: boolean;
	/** The grade carries a visual check (X1). */
	visual: boolean;
	/** The run has a voice call (X8). */
	voice: boolean;
	/** The run held GPUs (X7). */
	gpu: boolean;
	/** Services ran beside the agent (X2). */
	services: boolean;
}

/**
 * Tabs for evidence the run did not record are hidden, never shown empty. Security reads the lines
 * the agent added, so it applies to diff runs only. Overview, Timeline, Output and Evidence always apply.
 */
export function hiddenRunTabs(facts: RunFacts): ReadonlySet<RunTabId> {
	const hidden = new Set<RunTabId>();
	if (!facts.agents) hidden.add("agents");
	if (!facts.visual) hidden.add("visual");
	if (!facts.voice) hidden.add("voice");
	if (!facts.gpu) hidden.add("gpu");
	if (!facts.services) hidden.add("services");
	if (facts.output !== "diff") hidden.add("security");
	return hidden;
}

/** Whether a run can have a voice call: the server serves /voice only for a run that exported timeline.jsonl. */
export function runHasVoice(d: RunDetail | undefined): boolean {
	return d?.state?.some((f) => f.id === "timeline.jsonl") ?? false;
}

/** The part of a query result the tab flags read. */
interface TabQuery<T> {
	data?: T;
	isError?: boolean;
}

/**
 * Which of the agents, replay and recording tabs to show. A tab whose request failed is shown too,
 * so its body can say the load failed instead of the tab vanishing.
 */
export function runTabFlags(
	agents: TabQuery<{ available?: boolean }>,
	frames: TabQuery<{ frames: readonly unknown[] }>,
	recordings: TabQuery<{ recordings: readonly unknown[] }>,
): { agents: boolean; replay: boolean; recording: boolean } {
	return {
		agents: Boolean(agents.isError || agents.data?.available),
		replay: Boolean(frames.isError || frames.data?.frames.length),
		recording: Boolean(recordings.isError || recordings.data?.recordings.length),
	};
}