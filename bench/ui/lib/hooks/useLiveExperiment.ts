// One experiment followed live: GET /experiments/{id}/events in its JSON poll form every 2 s with
// ?since=, which carries both the new events and the current status. The server-sent form exists
// but carries no status and cannot send the org header, so polling is the one path that works
// everywhere (proxies that buffer streams, fixture mode, hosted orgs). Polling slows while the tab
// is hidden and stops after the experiment reaches a terminal state.
import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
import {
	elapsedBetween,
	initialLiveState,
	liveExperimentReducer,
	orderedSlots,
	type LiveSlot,
} from "@/components/live/liveExperiment.reducer";
import { EXPERIMENT_POLL_MS, useListRunsQuery } from "@/lib/store/apis/benchApi";
import { homeApi } from "@/lib/store/apis/homeApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { useEffect, useMemo, useReducer, useRef, useState } from "react";

const HIDDEN_POLL_MS = 15_000;
/** How often the task and arm names of new runs are looked up while live. */
const RUN_LOOKUP_MS = 10_000;

export interface LiveSlotView extends LiveSlot {
	task?: string;
	arm?: string;
	/** Milliseconds since the slot started, up to now or its finish. */
	elapsedMs?: number;
}

export interface LiveExperiment {
	status?: ExperimentStatus;
	/** Running slots first, then the latest finished ones. */
	slots: LiveSlotView[];
	/** Newest last. */
	events: LiveEvent[];
	latest?: LiveEvent;
	/** Milliseconds since the experiment started. */
	elapsedMs?: number;
	live: boolean;
	error?: unknown;
}

export interface UseLiveExperimentOptions {
	/** The status the page already has; shown until the first poll lands. */
	initial?: ExperimentStatus;
	/** False to only show `initial` without polling. */
	enabled?: boolean;
	/** How many finished slots stay listed under the running ones. */
	finishedKeep?: number;
	onFinished?: () => void;
}

/** A clock that ticks every second while `active`, for elapsed timers. */
export function useNow(active: boolean, everyMs = 1000): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		setNow(Date.now());
		const id = window.setInterval(() => setNow(Date.now()), everyMs);
		return () => window.clearInterval(id);
	}, [active, everyMs]);
	return now;
}

export function useLiveExperiment(expId: string | undefined, options: UseLiveExperimentOptions = {}): LiveExperiment {
	const { initial, enabled = true, finishedKeep = 4, onFinished } = options;
	const dispatch = useAppDispatch();
	const [state, send] = useReducer(liveExperimentReducer, initial, initialLiveState);
	const finished = useRef(onFinished);
	finished.current = onFinished;

	// A newer status from the page (its own polling) wins until our first poll lands.
	useEffect(() => {
		if (initial && initial.exp_id === expId) send({ type: "status", status: initial });
	}, [initial, expId]);

	const initialTerminal = initial ? TERMINAL_STATES.has(initial.state) : false;
	const shouldPoll = Boolean(expId) && enabled && !initialTerminal;

	useEffect(() => {
		if (!expId || !shouldPoll) return;
		send({ type: "reset", status: initial });
		let stopped = false;
		let timer: number | undefined;
		let since = 0;
		const poll = async () => {
			if (stopped) return;
			const result = await dispatch(
				homeApi.endpoints.getExperimentEvents.initiate({ expId, since }, { subscribe: false, forceRefetch: true }),
			);
			if (stopped) return;
			if (result.data) {
				since = Math.max(since, result.data.next);
				send({ type: "poll", page: result.data });
				if (TERMINAL_STATES.has(result.data.status.state)) {
					finished.current?.();
					return;
				}
			} else if (result.error) {
				send({ type: "error", error: result.error });
			}
			const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
			timer = window.setTimeout(() => void poll(), hidden ? HIDDEN_POLL_MS : EXPERIMENT_POLL_MS);
		};
		void poll();
		return () => {
			stopped = true;
			if (timer !== undefined) window.clearTimeout(timer);
		};
		// `initial` seeds the reset only; a new status object must not restart polling.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [dispatch, expId, shouldPoll]);

	const status = state.status ?? initial;
	const live = status ? !TERMINAL_STATES.has(status.state) && status.state !== "created" : false;
	const hasSlots = Object.keys(state.slots).length > 0;
	const runs = useListRunsQuery(expId ? { exp: expId, limit: 200 } : undefined, {
		skip: !expId || !hasSlots,
		pollingInterval: live ? RUN_LOOKUP_MS : 0,
		skipPollingIfUnfocused: true,
	});
	const now = useNow(live);

	const slots = useMemo(() => {
		const byRun = new Map((runs.data?.runs ?? []).map((r) => [r.run_id, r]));
		return orderedSlots(state, finishedKeep).map((slot): LiveSlotView => {
			const row = slot.runId ? byRun.get(slot.runId) : undefined;
			return { ...slot, task: row?.task, arm: row?.arm, elapsedMs: elapsedBetween(slot.startedAt, slot.finishedAt, now) };
		});
	}, [state, finishedKeep, runs.data, now]);

	return {
		status,
		slots,
		events: state.events,
		latest: state.events.length > 0 ? state.events[state.events.length - 1] : undefined,
		elapsedMs: status ? elapsedBetween(status.started_at, status.finished_at, now) : undefined,
		live,
		error: state.error,
	};
}
