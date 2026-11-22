import { JobLogPanel as LogLines } from "@/app/jobs/$jobId/views/jobView";
import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton, JobStatusBadge } from "@/components/platform/badges";
import { JobProgressBar } from "@/components/platform/jobsDrawer";
import { isActiveJob } from "@/components/platform/platform.utils";
import { useJobLog } from "@/components/platform/useJobLog";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCancelJobMutation, useGetJobQuery } from "@/lib/store/apis/platformApi";
import type { Job } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ExternalLink, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useJobKindName } from "./useJobKindName";

/** How often the panel refreshes a queued or running job. */
export const JOB_PANEL_POLL_MS = 2000;

export interface JobLogPanelProps {
	jobId: string;
	onClose?: () => void;
	/** Called once when the job reaches succeeded, failed or cancelled. */
	onFinished?: (job: Job) => void;
}

/** A job's status, progress and live log with Cancel, for use inline next to the button that started it. */
export function JobLogPanel({ jobId, onClose, onFinished }: JobLogPanelProps) {
	const { t } = useTranslation("jobs");
	const kindName = useJobKindName();
	const log = useJobLog(jobId);
	// The log stream can end a moment before the job row turns terminal, so
	// keep polling the row until it has (a stopped poll left "Running" up).
	const [rowDone, setRowDone] = useState(false);
	const job = useGetJobQuery(jobId, { pollingInterval: log.ended && rowDone ? 0 : JOB_PANEL_POLL_MS });
	useEffect(() => {
		setRowDone(Boolean(job.data && !isActiveJob(job.data)));
	}, [job.data]);
	// When the log ends while the row still says running, ask for the row at once instead of
	// waiting for the next poll, so the badge does not read "Running" under a finished log (N63).
	const refetchedOnEnd = useRef(false);
	const { refetch } = job;
	useEffect(() => {
		if (!refetchedOnEnd.current && refetchOnLogEnd(log.ended, job.data)) {
			refetchedOnEnd.current = true;
			void refetch();
		}
	}, [log.ended, job.data, refetch]);
	const [cancel, cancelState] = useCancelJobMutation();
	const data = job.data;
	const active = data ? isActiveJob(data) : true;

	const reported = useRef<string | null>(null);
	useEffect(() => {
		if (data && !isActiveJob(data) && reported.current !== data.id) {
			reported.current = data.id;
			onFinished?.(data);
		}
	}, [data, onFinished]);

	return (
		<section
			className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-4 shadow-sm"
			data-testid="job-log-panel"
			data-status={data?.status}
		>
			<header className="flex flex-wrap items-start gap-3">
				<div className="flex min-w-0 flex-col gap-1">
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-foreground text-base font-semibold tracking-tight">{data ? kindName(data.kind) : t("panel.title")}</span>
						{data && <JobStatusBadge status={data.status} />}
					</div>
					<span className="text-muted-foreground/80 max-w-60 truncate font-mono text-[11px]" title={jobId}>
						{jobId}
					</span>
				</div>
				<div className="ml-auto flex items-center gap-1">
					{data && active && !data.cancel_requested && (
						<RoleGate min="member" fallback={null}>
							<ConfirmButton
								label={t("panel.cancel")}
								question={t("confirmCancel")}
								confirmLabel={t("confirm")}
								cancelLabel={t("keep")}
								onConfirm={() => void cancel(jobId)}
								isLoading={cancelState.isLoading}
								variant="destructive"
								testId="job-log-panel-cancel"
							/>
						</RoleGate>
					)}
					<Button asChild variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs">
						<Link to="/jobs/$jobId" params={{ jobId }} data-testid="job-log-panel-open">
							<ExternalLink className="size-3.5" aria-hidden="true" />
							{t("panel.open")}
						</Link>
					</Button>
					{onClose && (
						<Button
							variant="ghost"
							size="sm"
							className="h-7 px-2"
							onClick={onClose}
							dataTestId="job-log-panel-close"
							aria-label={t("panel.close")}
						>
							<X className="size-3.5" aria-hidden="true" />
						</Button>
					)}
				</div>
			</header>
			{data && <JobProgressBar job={data} />}
			{data && <JobExperimentLink job={data} />}
			{data?.status === "queued" && <p className="text-muted-foreground text-sm">{t("panel.queued")}</p>}
			{data?.status === "failed" && (
				<p role="alert" className="border-chart-error/40 bg-chart-error/5 text-chart-error-ink rounded-lg border px-3 py-2 text-xs" data-testid="job-log-panel-error">
					{t("panel.failed")} {data.error}
				</p>
			)}
			{data?.status === "failed" && needsRunner(data.error) && (
				<p className="text-xs" data-testid="job-log-panel-add-runner">
					<Link to="/settings" search={{ tab: "runners" } as never} className="text-primary underline">
						{t("panel.addRunner")}
					</Link>
				</p>
			)}
			{cancelState.error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{t("actionError")}: {getBenchErrorMessage(cancelState.error)}
				</p>
			)}
			<LogLines log={log} active={active} />
		</section>
	);
}

/** Whether the job row should be asked for again: the log stream ended but the row is still queued or running. */
export function refetchOnLogEnd(logEnded: boolean, job: Job | undefined): boolean {
	return logEnded && job !== undefined && isActiveJob(job);
}

/** Exp id a job result names (bridge jobs on runners report exp_id), or null. */
export function jobResultExpId(job: Job): string | null {
	const id = job.result?.exp_id;
	return typeof id === "string" && id !== "" ? id : null;
}

function JobExperimentLink({ job }: { job: Job }) {
	const { t } = useTranslation("jobs");
	const expId = jobResultExpId(job);
	if (!expId) return null;
	return (
		<Link to="/experiments/$expId" params={{ expId }} className="text-primary text-xs underline" data-testid="job-log-panel-experiment">
			{t("panel.experiment", { expId })}
		</Link>
	);
}

export default JobLogPanel;

/** needsRunner reports a job that failed because no worker was online (runner jobs, gap G05). Older servers say "runner". */
export function needsRunner(error?: string | null): boolean {
	return !!error && /no (worker|runner) online/i.test(error);
}
