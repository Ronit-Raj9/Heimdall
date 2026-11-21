// One running experiment followed live: GET /experiments/{id}/events in its JSON form, polled
// with ?since= so each poll carries only new events plus the current status. Polling slows down
// while the tab is hidden and stops once the experiment reaches a terminal state.
import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
import { homeApi } from "@/lib/store/apis/homeApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { useEffect, useRef, useState } from "react";
import { mergeEvents } from "./home.utils";

/** Poll interval while the tab is hidden. */
const HIDDEN_POLL_MS = 15_000;
/** Poll interval while the tab is visible: the experiment progress endpoint every 3 s. */
export const LIVE_POLL_MS = 3_000;

export interface ExperimentEventsState {
	status: ExperimentStatus;
	events: LiveEvent[];
	error: unknown;
}

/** Follows one experiment, starting from the status the list already has. */
export function useExperimentEvents(initial: ExperimentStatus, onFinished?: () => void): ExperimentEventsState {
	const dispatch = useAppDispatch();
	const [state, setState] = useState<ExperimentEventsState>({ status: initial, events: [], error: undefined });
	const finished = useRef(onFinished);
	finished.current = onFinished;
	const expId = initial.exp_id;

	useEffect(() => {
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
				const page = result.data;
				since = Math.max(since, page.next);
				setState((prev) => ({ status: page.status, events: mergeEvents(prev.events, page.events), error: undefined }));
				if (TERMINAL_STATES.has(page.status.state)) {
					finished.current?.();
					return;
				}
			} else if (result.error) {
				setState((prev) => ({ ...prev, error: result.error }));
			}
			const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
			timer = window.setTimeout(() => void poll(), hidden ? HIDDEN_POLL_MS : LIVE_POLL_MS);
		};
		void poll();
		return () => {
			stopped = true;
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [dispatch, expId]);

	return state;
}