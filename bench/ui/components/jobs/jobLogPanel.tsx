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
import { useEffect, useRef } from "react";
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
	const job = useGetJobQuery(jobId, { pollingInterval: log.ended ? 0 : JOB_PANEL_POLL_MS });
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
		<section className="border-border flex min-w-0 flex-col gap-3 rounded-lg border p-3" data-testid="job-log-panel" data-status={data?.status}>
			<header className="flex flex-wrap items-center gap-2 text-sm">
				<span className="text-foreground font-medium">{data ? kindName(data.kind) : t("panel.title")}</span>
				{data && <JobStatusBadge status={data.status} />}
				<span className="text-muted-foreground font-mono text-xs">{jobId}</span>
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
			{data?.status === "queued" && <p className="text-muted-foreground text-xs">{t("panel.queued")}</p>}
			{data?.status === "failed" && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="job-log-panel-error">
					{t("panel.failed")} {data.error}
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

export default JobLogPanel;
