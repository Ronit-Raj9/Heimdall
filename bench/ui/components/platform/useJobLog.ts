// A job's log as it grows: server-sent events from GET /jobs/{id}/logs (one "log" event per line,
// an "end" event once the job ended and every line went out), falling back to polling the same
// route as JSON when the stream is not available (fixture mode, a proxy that buffers, an old
// browser).
import { useAppDispatch } from "@/lib/store/hooks";
import { IS_FIXTURE_MODE } from "@/lib/store/apis/benchApi";
import { JOB_LOG_POLL_MS, jobLogStreamUrl, platformApi } from "@/lib/store/apis/platformApi";
import type { JobLogLine, JobStatus } from "@/lib/types/bench";
import { useEffect, useRef, useState } from "react";

/** Lines kept in the page; older ones scroll out. */
export const JOB_LOG_KEEP = 5000;

export interface JobLogState {
	lines: JobLogLine[];
	/** Lines the server left out to keep the log bounded. */
	dropped: number;
	/** The job ended and every line arrived. */
	ended: boolean;
	/** How lines arrive. */
	transport: "stream" | "poll";
	error: unknown;
}

/** Appends lines after the last seq seen, keeping order and the last JOB_LOG_KEEP lines. */
export function appendLines(current: readonly JobLogLine[], incoming: readonly JobLogLine[]): JobLogLine[] {
	const last = current.length > 0 ? current[current.length - 1].seq : -1;
	const fresh = incoming.filter((line) => line.seq > last).sort((a, b) => a.seq - b.seq);
	if (fresh.length === 0) return current as JobLogLine[];
	const next = [...current, ...fresh];
	return next.length > JOB_LOG_KEEP ? next.slice(next.length - JOB_LOG_KEEP) : next;
}

const TERMINAL: ReadonlySet<JobStatus> = new Set(["succeeded", "failed", "cancelled"]);

/** Follows one job's log until it ends. */
export function useJobLog(jobId: string): JobLogState {
	const dispatch = useAppDispatch();
	const [state, setState] = useState<JobLogState>({ lines: [], dropped: 0, ended: false, transport: "stream", error: undefined });
	const cursor = useRef(0);

	useEffect(() => {
		cursor.current = 0;
		setState({ lines: [], dropped: 0, ended: false, transport: "stream", error: undefined });
		let stopped = false;
		let timer: number | undefined;
		let source: EventSource | undefined;

		const take = (lines: JobLogLine[], extra: Partial<JobLogState> = {}) => {
			if (lines.length > 0) cursor.current = Math.max(cursor.current, lines[lines.length - 1].seq);
			setState((prev) => ({ ...prev, ...extra, lines: appendLines(prev.lines, lines) }));
		};

		const poll = async () => {
			if (stopped) return;
			const request = dispatch(
				platformApi.endpoints.getJobLogs.initiate({ jobId, since: cursor.current }, { subscribe: false, forceRefetch: true }),
			);
			const result = await request;
			if (stopped) return;
			if (result.data) {
				const page = result.data;
				cursor.current = Math.max(cursor.current, page.next);
				const ended = TERMINAL.has(page.status) && page.lines.length === 0;
				take(page.lines, { dropped: page.dropped, ended, transport: "poll", error: undefined });
				if (ended) return;
			} else if (result.error) {
				setState((prev) => ({ ...prev, transport: "poll", error: result.error }));
			}
			timer = window.setTimeout(() => void poll(), JOB_LOG_POLL_MS);
		};

		const canStream = !IS_FIXTURE_MODE && typeof window !== "undefined" && typeof window.EventSource === "function";
		if (!canStream) {
			void poll();
		} else {
			let opened = false;
			source = new EventSource(jobLogStreamUrl(jobId, 0), { withCredentials: true });
			source.addEventListener("open", () => {
				opened = true;
			});
			source.addEventListener("log", (event) => {
				try {
					take([JSON.parse((event as MessageEvent<string>).data) as JobLogLine], { transport: "stream", error: undefined });
				} catch {
					// A line that is not JSON is skipped; the next poll or event carries on.
				}
			});
			source.addEventListener("end", (event) => {
				let dropped = 0;
				try {
					dropped = Number((JSON.parse((event as MessageEvent<string>).data) as { dropped?: number }).dropped ?? 0);
				} catch {
					// No body: nothing was dropped.
				}
				setState((prev) => ({ ...prev, dropped, ended: true }));
				source?.close();
			});
			source.addEventListener("error", () => {
				// Never opened (no stream support, a 401, a buffering proxy): poll the JSON form instead.
				// After it opened, EventSource reconnects by itself with Last-Event-ID.
				if (!opened || source?.readyState === EventSource.CLOSED) {
					source?.close();
					source = undefined;
					void poll();
				}
			});
		}

		return () => {
			stopped = true;
			source?.close();
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [dispatch, jobId]);

	return state;
}