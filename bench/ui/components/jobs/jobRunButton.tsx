import { useConfirm } from "@/components/platform/confirmDialog";
import { Button, type buttonVariants } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import type { Job } from "@/lib/types/bench";
import type { VariantProps } from "class-variance-authority";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { JobLogPanel } from "./jobLogPanel";

export interface JobRunButtonProps {
	kind: string;
	params?: object;
	label: string;
	/** When set, asked in the in-page confirm dialog before the job is queued. */
	confirm?: string;
	/** Disables the button (for example until a required consent is given). */
	disabled?: boolean;
	/** Called once the job finished (succeeded, failed or cancelled). */
	onDone?(job: Job): void;
	variant?: VariantProps<typeof buttonVariants>["variant"];
}

/** A job start error without the server's fixed "create job: invalid job parameters: " lead (fix wave 3, N238; wave 2 D3). */
export function plainJobError(msg: string): string {
	return msg.replace(/^create job: invalid job parameters: /, "");
}

/**
 * Queues a job through POST /jobs and opens its live log with Cancel beneath the button. Viewers
 * see the button disabled; a refused or invalid request shows the server's message.
 */
export function JobRunButton({ kind, params, label, confirm, disabled, onDone, variant }: JobRunButtonProps) {
	const { t } = useTranslation("jobs");
	const session = useSession();
	const askConfirm = useConfirm();
	const [createJob, { isLoading, error, reset }] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const allowed = canUse(session, "member");
	const run = async () => {
		if (confirm && !(await askConfirm(confirm))) return;
		reset();
		const res = await createJob({ kind, params });
		if ("data" in res && res.data) setJobId(res.data.id);
	};
	const finished = useCallback((job: Job) => onDone?.(job), [onDone]);
	const busy = isLoading;
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={`job-run-${kind}-wrap`}>
			<div>
				<Button variant={variant} disabled={!allowed || busy || !!disabled} isLoading={busy} onClick={() => void run()} dataTestId={`job-run-${kind}`}>
					{label}
				</Button>
			</div>
			{error !== undefined && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid={`job-run-${kind}-error`} title={getBenchErrorMessage(error)}>
					{t("panel.startError")}: {plainJobError(getBenchErrorMessage(error))}
				</p>
			)}
			{jobId && <JobLogPanel jobId={jobId} onClose={() => setJobId(null)} onFinished={finished} />}
		</div>
	);
}

export default JobRunButton;
