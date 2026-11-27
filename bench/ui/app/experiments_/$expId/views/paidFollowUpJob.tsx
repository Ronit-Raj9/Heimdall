import { plainJobError } from "@/components/jobs/jobRunButton";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { followUpOf } from "@/lib/types/experimentActions";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

export interface PaidFollowUpJobProps {
	kind: string;
	/** Null renders a disabled button with the reason in `missing`. */
	params: object | null;
	/** The verb phrase, for example "Extend". */
	label: string;
	/** One more sentence for the confirm: what it starts (runs, price). */
	confirm: string;
	missing?: string;
	estimateUsd?: number | null;
	estimateRuns?: number | null;
	testId: string;
}

/**
 * A follow-up job that starts paid runs (M4-33, M4-41): the shared PaidButton (label with the cost,
 * the estimate under it, always a confirm with the amount), then the job's live log and, once it
 * ends, a link to the comparison it made. Same test ids as FollowUpJob and JobRunButton.
 */
export function PaidFollowUpJob({ kind, params, label, confirm, missing, estimateUsd, estimateRuns, testId }: PaidFollowUpJobProps) {
	const { t } = useTranslation("experimentActions");
	const { t: tj } = useTranslation("jobs");
	const session = useSession();
	const [createJob, { isLoading, error, reset }] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const [doneId, setDoneId] = useState<string | null>(null);
	const job = useGetJobQuery(doneId ?? skipToken, { pollingInterval: 2000, skipPollingIfUnfocused: true });
	const status = job.data?.status;
	const done = status ? TERMINAL.has(status) : false;
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
				{params ? (
					<div className="flex min-w-0 flex-col gap-2" data-testid={`job-run-${kind}-wrap`}>
						<div>
							<PaidButton
								verb={label}
								capUsd={null}
								estimateUsd={estimateUsd}
								estimateRuns={estimateRuns ?? undefined}
								confirm="always"
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