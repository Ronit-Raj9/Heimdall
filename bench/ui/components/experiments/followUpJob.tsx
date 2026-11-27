import { JobRunButton, plainJobError } from "@/components/jobs/jobRunButton";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { followUpOf } from "@/lib/types/experimentActions";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { JOB_ENDED, useLiveJob } from "./liveJob";

export interface FollowUpJobProps {
	kind: string;
	/** Null renders a disabled button with the reason in `missing`. */
	params: object | null;
	/** The verb phrase, for example "Extend". */
	label: string;
	/** One more sentence for the confirm: what it starts (runs, price). */
	confirm: string;
	missing?: string;
	/**
	 * Set when the job starts paid runs (M4-33, M4-41): the shared PaidButton with the cost, the
	 * estimate under it and always a confirm with the amount. Unset: the plain JobRunButton.
	 */
	paid?: { estimateUsd?: number | null; estimateRuns?: number | null };
	testId: string;
}

/**
 * Queues one follow-up job (experiment.extend, experiment.migrate, probe.run) and, once it ends,
 * links the experiment it created. params null renders a disabled button with the reason. With
 * `paid` the start is the shared PaidButton and the job's live log shows under it.
 */
export function FollowUpJob({ kind, params, label, confirm, missing, paid, testId }: FollowUpJobProps) {
	const { t } = useTranslation("experimentActions");
	const { t: tj } = useTranslation("jobs");
	const session = useSession();
	const [createJob, { isLoading, error, reset }] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const [doneId, setDoneId] = useState<string | null>(null);
	// M1-62: a live job polls every 2 s, a finished one never again.
	const job = useLiveJob(doneId, 2000);
	const status = job.data?.status;
	const done = status ? JOB_ENDED.has(status) : false;
	const follow = done ? followUpOf(job.data?.result) : null;
	const run = async () => {
		reset?.();
		const res = await createJob({ kind, params: params ?? undefined });
		if ("data" in res && res.data) {
			setJobId(res.data.id);
			notify({ text: t("started", { ns: "toast", name: label }) });
		}
	};
	return (
		<div className="flex flex-col gap-2" data-testid={testId}>
			<div className="flex flex-wrap items-center gap-3">
				{params && !paid ? (
					<JobRunButton kind={kind} params={params} label={label} confirm={confirm} onDone={(j) => setDoneId(j.id)} />
				) : params && paid ? (
					<div className="flex min-w-0 flex-col gap-2" data-testid={`job-run-${kind}-wrap`}>
						<div>
							<PaidButton
								verb={label}
								capUsd={null}
								estimateUsd={paid.estimateUsd}
								estimateRuns={paid.estimateRuns ?? undefined}
								confirm="auto"
								confirmText={confirm}
								disabled={!canUse(session, "member")}
								isLoading={isLoading}
								onConfirm={run}
								testId={`job-run-${kind}`}
							/>
						</div>
						{error !== undefined && (
							<p role="alert" className="text-danger-ink text-xs" data-testid={`job-run-${kind}-error`} title={getBenchErrorMessage(error)}>
								{tj("panel.startError")}: {plainJobError(getBenchErrorMessage(error))}
							</p>
						)}
						{jobId && <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} onFinished={(j) => setDoneId(j.id)} />}
					</div>
				) : (
					<Button disabled data-testid={`${testId}-disabled`}>
						{label}
					</Button>
				)}
				{!params && missing && <span className="text-muted-foreground text-xs">{missing}</span>}
				{doneId && !done && status && <span className="text-muted-foreground text-xs">{t("job.running", { status })}</span>}
			</div>
			{status === "failed" && (
				<p className="text-danger-ink text-sm" role="alert" data-testid={`${testId}-error`}>
					{t("job.failed", { error: job.data?.error ?? "" })}
				</p>
			)}
			{follow && (
				<div className="flex flex-wrap items-center gap-3 text-sm" data-testid={`${testId}-result`}>
					<Button variant="outline" size="sm" asChild>
						<Link to="/experiments/$expId" params={{ expId: follow.exp_id }}>
							{t("job.open", { expId: follow.exp_id })}
						</Link>
					</Button>
					{!follow.started && <span className="text-muted-foreground text-xs">{t("job.notStarted")}</span>}
				</div>
			)}
		</div>
	);
}