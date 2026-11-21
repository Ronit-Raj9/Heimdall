// The pure state behind useLiveExperiment: the experiment status plus a per-slot view folded from
// its live event stream (slot_started, run_started, stage, run_sealed / slot_sealed, slot_skipped,
// error). Remote runners forward run_started and stage events to the hosted server, so a remote
// slot moves through its stages here exactly like a local one.
import type { EventsPoll, ExperimentStatus, LiveEvent, Outcome, Stage } from "@/lib/types/bench";

/** Events kept for the ticker. */
export const LIVE_EVENTS_KEEP = 30;
/** Slots kept in the per-slot view; the oldest finished ones drop first. */
export const LIVE_SLOTS_KEEP = 200;

export type LiveSlotState = "running" | "done" | "failed" | "skipped";

export interface LiveSlot {
	slotId: string;
	runId?: string;
	state: LiveSlotState;
	/** The newest stage the slot reported. */
	stage?: Stage;
	outcome?: Outcome;
	/** The newest message the slot reported. */
	message?: string;
	startedAt: string;
	updatedAt: string;
	finishedAt?: string;
	/** Order of first sighting, so rows stay put while they update. */
	order: number;
}

export interface LiveExperimentState {
	status?: ExperimentStatus;
	events: LiveEvent[];
	slots: Record<string, LiveSlot>;
	/** run_id to slot_id, for events that carry only the run. */
	runs: Record<string, string>;
	/** The ?since= of the next poll. */
	since: number;
	error?: unknown;
	counter: number;
}

export type LiveAction =
	| { type: "poll"; page: Pick<EventsPoll, "events" | "next" | "status"> }
	| { type: "events"; events: readonly LiveEvent[] }
	| { type: "status"; status: ExperimentStatus }
	| { type: "error"; error: unknown }
	| { type: "reset"; status?: ExperimentStatus };

export function initialLiveState(status?: ExperimentStatus): LiveExperimentState {
	return { status, events: [], slots: {}, runs: {}, since: 0, counter: 0 };
}

/** Outcomes that mean the slot could not be scored (infrastructure, evaluator), shown as failed. */
const FAILED_OUTCOMES: ReadonlySet<string> = new Set(["infra_error", "aborted_infra", "invalid_evaluator"]);

const STARTED_KINDS = new Set(["slot_started", "run_started", "run_recovered", "lease_held"]);
const SEALED_KINDS = new Set(["slot_sealed", "run_sealed"]);

/** Applies one event to the slot map. Returns the same objects when nothing changed. */
function applyEvent(state: LiveExperimentState, ev: LiveEvent): LiveExperimentState {
	const slotId = ev.slot_id ?? (ev.run_id ? state.runs[ev.run_id] : undefined);
	if (!slotId) return state;
	const prev = state.slots[slotId];
	let counter = state.counter;
	const base: LiveSlot = prev ?? { slotId, state: "running", startedAt: ev.at, updatedAt: ev.at, order: counter++ };
	const next: LiveSlot = { ...base, updatedAt: ev.at };
	if (ev.run_id) next.runId = ev.run_id;
	if (ev.message) next.message = ev.message;

	if (STARTED_KINDS.has(ev.kind)) {
		// A retry or recovery reopens a finished slot.
		if (next.state !== "running") {
			next.state = "running";
			next.finishedAt = undefined;
			next.outcome = undefined;
		}
	} else if (ev.kind === "stage") {
		if (ev.stage) next.stage = ev.stage;
		if (next.state !== "running" && !next.finishedAt) next.state = "running";
	} else if (SEALED_KINDS.has(ev.kind)) {
		next.outcome = ev.outcome;
		next.state = ev.outcome && FAILED_OUTCOMES.has(ev.outcome) ? "failed" : "done";
		next.stage = "seal";
		next.finishedAt = ev.at;
	} else if (ev.kind === "slot_skipped") {
		next.state = "skipped";
		next.finishedAt = ev.at;
	} else if (ev.kind === "error") {
		next.state = "failed";
		next.finishedAt = ev.at;
	}

	const runs = ev.run_id && state.runs[ev.run_id] !== slotId ? { ...state.runs, [ev.run_id]: slotId } : state.runs;
	return { ...state, slots: { ...state.slots, [slotId]: next }, runs, counter };
}

/** Drops the oldest finished slots once the map grows past `keep`. */
function trimSlots(slots: Record<string, LiveSlot>, keep: number): Record<string, LiveSlot> {
	const all = Object.values(slots);
	if (all.length <= keep) return slots;
	const finished = all.filter((s) => s.state !== "running").sort((a, b) => a.order - b.order);
	const drop = new Set(finished.slice(0, all.length - keep).map((s) => s.slotId));
	return Object.fromEntries(all.filter((s) => !drop.has(s.slotId)).map((s) => [s.slotId, s]));
}

function applyEvents(state: LiveExperimentState, incoming: readonly LiveEvent[]): LiveExperimentState {
	const last = state.events.length > 0 ? state.events[state.events.length - 1].seq : -1;
	// Events without a seq (unnumbered SSE) are always new; numbered ones only past the newest seen.
	const fresh = incoming.filter((ev) => !(ev.seq > 0) || ev.seq > last).sort((a, b) => (a.seq || 0) - (b.seq || 0));
	if (fresh.length === 0) return state;
	let next = state;
	for (const ev of fresh) next = applyEvent(next, ev);
	const events = [...state.events, ...fresh];
	return {
		...next,
		events: events.length > LIVE_EVENTS_KEEP ? events.slice(events.length - LIVE_EVENTS_KEEP) : events,
		slots: trimSlots(next.slots, LIVE_SLOTS_KEEP),
	};
}

export function liveExperimentReducer(state: LiveExperimentState, action: LiveAction): LiveExperimentState {
	switch (action.type) {
		case "poll": {
			const next = applyEvents(state, action.page.events);
			return { ...next, status: action.page.status, since: Math.max(state.since, action.page.next), error: undefined };
		}
		case "events":
			return applyEvents(state, action.events);
		case "status":
			return { ...state, status: action.status };
		case "error":
			return { ...state, error: action.error };
		case "reset":
			return initialLiveState(action.status);
	}
}

/** Running slots first (newest first), then the most recently finished ones. */
export function orderedSlots(state: Pick<LiveExperimentState, "slots">, finishedKeep = 4): LiveSlot[] {
	const all = Object.values(state.slots);
	const running = all.filter((s) => s.state === "running").sort((a, b) => b.order - a.order);
	const finished = all
		.filter((s) => s.state !== "running")
		.sort((a, b) => (b.finishedAt ?? "").localeCompare(a.finishedAt ?? "") || b.order - a.order)
		.slice(0, finishedKeep);
	return [...running, ...finished];
}

/** Milliseconds as "12s", "3m 05s" or "2h 04m". */
export function formatElapsed(ms: number): string {
	const total = Math.max(0, Math.floor(ms / 1000));
	const h = Math.floor(total / 3600);
	const m = Math.floor((total % 3600) / 60);
	const s = total % 60;
	const pad = (n: number) => String(n).padStart(2, "0");
	if (h > 0) return `${h}h ${pad(m)}m`;
	if (m > 0) return `${m}m ${pad(s)}s`;
	return `${s}s`;
}

/** Elapsed time between an ISO start and an ISO end (or now). Undefined when the start is unreadable. */
export function elapsedBetween(start: string | undefined, end: string | undefined, now: number): number | undefined {
	if (!start) return undefined;
	const from = Date.parse(start);
	if (!Number.isFinite(from)) return undefined;
	const to = end ? Date.parse(end) : now;
	return Math.max(0, (Number.isFinite(to) ? to : now) - from);
}
