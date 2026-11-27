import { formatUsd } from "@/lib/money";
import { targetLabel } from "@/components/jobs/jobKindLabels";
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

/** The spend cap stored with the job in dollars, or null when it has none (PaidButton cap, M2-1). */
export function jobBudgetUsd(job: Pick<Job, "params">): number | null {
	const v = job.params?.budget_usd;
	return typeof v === "number" && Number.isFinite(v) && v > 0 ? v : null;
}

/** True for a job about a test object: its comparison name or target (prefix dropped) is a test name (M1-18). */
export function isTestJob(job: Pick<Job, "target" | "params">, isTest: (name: string) => boolean): boolean {
	const name = typeof job.params?.name === "string" ? job.params.name : "";
	return (name !== "" && isTest(name)) || isTest(targetLabel(job.target));
}

/** The first line of a job's error, for the one-line reason under a Failed badge (M2-3). */
export function firstErrorLine(error: string | undefined | null): string {
	return (error ?? "").split("\n")[0].trim();
}

/** Filter values from the URL: unknown statuses are dropped (M2-5). */
export function parseJobStatus<S extends string>(value: string, statuses: readonly S[]): S | "" {
	return (statuses as readonly string[]).includes(value) ? (value as S) : "";
}

/** Jobs list poll: every 5 s while a listed job is queued or running, else a slow 30 s refresh for jobs started elsewhere (M1-62). */
export function jobsPollMs(anyActive: boolean): number {
	return anyActive ? 5000 : 30_000;
}
