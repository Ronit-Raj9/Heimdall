import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Button } from "@/components/ui/button";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { followUpOf } from "@/lib/types/experimentActions";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

/**
 * Queues one follow-up job (experiment.extend, experiment.migrate, probe.run) and, once it ends,
 * links the experiment it created. params null renders a disabled button with the reason.
 */
export function FollowUpJob({ kind, params, label, confirm, missing, testId }: { kind: string; params: object | null; label: string; confirm: string; missing?: string; testId: string }) {
	const { t } = useTranslation("experimentActions");
	const [jobId, setJobId] = useState<string | null>(null);
	const job = useGetJobQuery(jobId ?? skipToken, { pollingInterval: 2000, skipPollingIfUnfocused: true });
	const status = job.data?.status;
	const done = status ? TERMINAL.has(status) : false;
	const follow = done ? followUpOf(job.data?.result) : null;
	return (
		<div className="flex flex-col gap-2" data-testid={testId}>
			<div className="flex flex-wrap items-center gap-3">
				{params ? (
					<JobRunButton kind={kind} params={params} label={label} confirm={confirm} onDone={(j) => setJobId(j.id)} />
				) : (
					<Button disabled data-testid={`${testId}-disabled`}>
						{label}
					</Button>
				)}
				{!params && missing && <span className="text-muted-foreground text-xs">{missing}</span>}
				{jobId && !done && status && <span className="text-muted-foreground text-xs">{t("job.running", { status })}</span>}
			</div>
			{status === "failed" && (
				<p className="text-destructive text-sm" role="alert" data-testid={`${testId}-error`}>
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
