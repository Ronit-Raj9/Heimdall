import type { ImprovementStatus } from "@/lib/types/improve";

/** Improvement states that still change on their own; any other state is final until someone acts (M1-62). */
const LIVE: readonly ImprovementStatus[] = ["planned", "running", "awaiting_approval"];

/** True while an improvement run can still change without a click. */
export function isLiveImprovement(status: ImprovementStatus | undefined): boolean {
	return status !== undefined && LIVE.includes(status);
}

/** Detail poll: every 5 s while the run is live, off once it is final (M1-62). */
export function improvementPollMs(status: ImprovementStatus | undefined): number {
	return status === undefined || isLiveImprovement(status) ? 5000 : 0;
}

/** List poll: every 5 s while a row can still change, else a slow 30 s refresh for runs started elsewhere (M1-62). */
export function listPollMs(anyLive: boolean): number {
	return anyLive ? 5000 : 30_000;
}
