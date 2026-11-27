import { formatUsd } from "@/lib/money";
import type { Job } from "@/lib/types/bench";

/** Retry is offered only for jobs that did not finish: failed or cancelled (N8-1). */
export function canRetryJob(job: Pick<Job, "status">): boolean {
	return job.status === "failed" || job.status === "cancelled";
}

/** The spend cap stored with the job, formatted, or null when the job has none (M2-1). */
export function jobBudget(job: Pick<Job, "params">): string | null {
	const v = job.params?.budget_usd;
	return typeof v === "number" && Number.isFinite(v) && v > 0 ? formatUsd(v) : null;
}

/**
 * The target without its internal prefix ("exp:", "suite:", "canary:", "onboard:"), and "" for
 * the search index, which needs no name (M8-58).
 */
export function jobTargetLabel(target: string | undefined | null): string {
	if (!target || target === "index") return "";
	return target.replace(/^(exp|suite|canary|onboard):/, "");
}

/** The first line of a job's error, for the one-line reason under a Failed badge (M2-3). */
export function firstErrorLine(error: string | undefined | null): string {
	return (error ?? "").split("\n")[0].trim();
}

/** Filter values from the URL: unknown statuses are dropped (M2-5). */
export function parseJobStatus<S extends string>(value: string, statuses: readonly S[]): S | "" {
	return (statuses as readonly string[]).includes(value) ? (value as S) : "";
}
