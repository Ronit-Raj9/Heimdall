import { PauseControls } from "@/components/experiments/runControls";
import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { ForbiddenNote, isRoleRefusal } from "@/components/platform/forbiddenNote";
import { RegradeDialog } from "@/components/platform/regradeDialog";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { useSession } from "@/lib/session/sessionContext";
import { useAppDispatch } from "@/lib/store";
import {
	benchApi,
	getBenchErrorMessage,
	useGetHealthQuery,
	useRegradeMutation,
	useStartExperimentMutation,
} from "@/lib/store/apis/benchApi";
import { useCancelExperimentMutation, useResumeExperimentMutation } from "@/lib/store/apis/platformApi";
import { launchAvailable, type ExperimentStatus, type Job, type Role } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ArrowRight, RotateCcw } from "lucide-react";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

/** A failed experiment action: the role refusal in hosted mode, the server's reason otherwise. */
function ActionError({ error, title, testId, min = "member" }: { error: unknown; title: string; testId: string; min?: Role }) {
	const session = useSession();
	if (isRoleRefusal(session, error)) return <ForbiddenNote min={min} error={error} testId={testId} className="w-full" />;
	return (
		<p role="alert" className="text-danger-ink w-full text-xs" data-testid={testId}>
			{title}: {getBenchErrorMessage(error)}
		</p>
	);
}

/**
 * Cancel a running experiment, resume a cancelled or failed one (POST /experiments/{id}/cancel and
 * /resume, served by the job runner), or start a frozen one that never started (POST /experiments/{id}/start). Members and up; demo visitors see nothing.
 */
export function ExperimentRunActions({ status }: { status: ExperimentStatus }) {
	const { t } = useTranslation("platform");
	const [cancel, cancelState] = useCancelExperimentMutation();
	const [resume, resumeState] = useResumeExperimentMutation();
	const [start, startState] = useStartExperimentMutation();
	const health = useGetHealthQuery();
	const running = status.state === "running";
	const resumable = status.state === "cancelled" || status.state === "failed";
	// Frozen but never started (for example a create-and-start whose start failed): start it again
	// under the same experiment ID. Only where the server has a launcher: offline serve answers 503
	// (QC2-27-03).
	const startable = status.state === "created" && launchAvailable(health.data);
	if (!running && !resumable && !startable) return null;
	return (
		<RoleGate min="member">
			<div className="flex flex-wrap items-center gap-2" data-testid="experiment-run-actions">
				{running && <PauseControls expId={status.exp_id} />}
				{running && (
					<ConfirmButton
						label={t("experimentActions.cancel")}
						question={t("experimentActions.confirmCancel", { id: status.exp_id })}
						confirmLabel={t("experimentActions.confirm")}
						cancelLabel={t("experimentActions.keep")}
						onConfirm={() => void cancel(status.exp_id)}
						isLoading={cancelState.isLoading}
						className="h-8 px-3 text-sm"
						testId="experiment-cancel"
					/>
				)}
				{/* M4-41: Resume and Start spend money on model calls: the shared PaidButton asks first. */}
				{resumable && (
					<PaidButton
						variant="outline"
						size="sm"
						verb={t("experimentActions.resume")}
						capUsd={null}
						confirm="auto"
						kind="form"
						isLoading={resumeState.isLoading}
						onConfirm={async () => {
							const res = await resume(status.exp_id);
							if ("data" in res) notify({ text: t("started", { ns: "toast", name: t("experimentActions.resume") }) });
						}}
						testId="experiment-resume"
					/>
				)}
				{startable && (
					<PaidButton
						size="sm"
						verb={t("experimentActions.start")}
						capUsd={null}
						confirm="auto"
						kind="form"
						isLoading={startState.isLoading}
						onConfirm={async () => {
							const res = await start(status.exp_id);
							if ("data" in res) notify({ text: t("started", { ns: "toast", name: t("experimentActions.start") }) });
						}}
						testId="experiment-start"
					/>
				)}
				{startState.error && (
					<ActionError error={startState.error} title={t("experimentActions.startError")} testId="experiment-start-error" />
				)}
				{cancelState.error && (
					<ActionError error={cancelState.error} title={t("experimentActions.cancelError")} testId="experiment-cancel-error" />
				)}
				{resumeState.error && (
					<ActionError error={resumeState.error} title={t("experimentActions.resumeError")} testId="experiment-resume-error" />
				)}
			</div>
		</RoleGate>
	);
}

/**
 * Regrade an experiment's sealed runs as a job (POST /experiments/{id}/regrade). The button opens a
 * dialog with the regate options (suite lock, note, regrade id); once queued, the job's live log
 * shows here with a link to read the verdict at the new grade source. The original grades stay.
 */
export function RegradeAction({
	expId,
	defaultOpen = false,
	open: openProp,
	onOpenChange,
	hideButton = false,
}: {
	expId: string;
	defaultOpen?: boolean;
	/** Controlled dialog (M4-14: the header More menu opens it); leave out for the button's own state. */
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
	/** The More menu holds the trigger: show only the started job, its log and the dialog. */
	hideButton?: boolean;
}) {
	const { t } = useTranslation("platform");
	const [regrade, state] = useRegradeMutation();
	const [ownOpen, setOwnOpen] = useState(defaultOpen);
	const open = openProp ?? ownOpen;
	const setOpen = useCallback(
		(next: boolean) => {
			setOwnOpen(next);
			onOpenChange?.(next);
		},
		[onOpenChange],
	);
	const [showLog, setShowLog] = useState(true);
	const close = useCallback(() => setOpen(false), [setOpen]);
	const job = state.data && state.originalArgs?.exp_id === expId ? state.data : null;
	const regradeId = job ? regradeIdOf(job) : null;
	// The mutation's job is the queued row; the log panel reports the finished one. A finished re-score
	// re-reads the status, whose latest_regrade the page then shows by default (gradeChoice.tsx).
	const [finished, setFinished] = useState<Job | null>(null);
	const dispatch = useAppDispatch();
	const onFinished = useCallback(
		(done: Job) => {
			setFinished(done);
			if (done.status === "succeeded") dispatch(benchApi.util.invalidateTags([{ type: "BenchExperiment", id: expId }]));
		},
		[dispatch, expId],
	);
	const succeeded = Boolean(job && (job.status === "succeeded" || (finished?.id === job.id && finished.status === "succeeded")));
	return (
		<RoleGate min="member" fallback={null}>
			<div className="flex min-w-0 flex-col items-end gap-2" data-testid="regrade-action">
				<div className="flex flex-wrap items-center justify-end gap-2">
					{job && (
						<span className="flex items-center gap-2 text-xs" role="status" data-testid="regrade-started">
							<span className="text-muted-foreground">{t("experimentActions.started", { id: job.id })}</span>
							<Link
								to="/jobs/$jobId"
								params={{ jobId: job.id }}
								className="text-primary inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline"
								data-testid="regrade-open-job"
							>
								{t("experimentActions.openJob")}
								<ArrowRight className="size-3" aria-hidden="true" />
							</Link>
						</span>
					)}
					{!hideButton && (
						<Button
							variant="outline"
							size="sm"
							className="gap-1.5"
							title={t("experimentActions.regradeHint")}
							isLoading={state.isLoading}
							onClick={() => setOpen(true)}
							dataTestId="regrade-button"
						>
							<RotateCcw className="size-3.5" aria-hidden="true" />
							{job ? t("experimentActions.regradeDialog.again") : t("experimentActions.regrade")}
						</Button>
					)}
				</div>
				{state.error && <ActionError error={state.error} title={t("experimentActions.regradeError")} testId="regrade-error" />}
				{job && showLog && (
					<div className="w-full max-w-xl text-left" data-testid="regrade-log">
						<JobLogPanel jobId={job.id} onClose={() => setShowLog(false)} onFinished={onFinished} />
						{regradeId && succeeded && (
							<Link
								to="/experiments/$expId"
								params={{ expId }}
								search={{ tab: "verdict", grade: regradeId }}
								className="text-primary mt-2 inline-flex items-center gap-1 text-xs font-medium underline-offset-4 hover:underline"
								data-testid="regrade-open-verdict"
							>
								{t("experimentActions.regradeDialog.openVerdict", { id: regradeId })}
								<ArrowRight className="size-3" aria-hidden="true" />
							</Link>
						)}
					</div>
				)}
				{open && (
					<RegradeDialog
						expId={expId}
						isLoading={state.isLoading}
						onClose={close}
						onSubmit={(body) => {
							setShowLog(true);
							void regrade({ exp_id: expId, ...body });
							setOpen(false);
						}}
					/>
				)}
			</div>
		</RoleGate>
	);
}

/** The regrade id an experiment.regate job writes: its params' id (the server fills it in when queued), else its result's. */
export function regradeIdOf(job: Job): string | null {
	const fromParams = job.params?.id;
	if (typeof fromParams === "string" && fromParams) return fromParams;
	const result = job.result as { regrade_id?: unknown } | null | undefined;
	return typeof result?.regrade_id === "string" && result.regrade_id ? result.regrade_id : null;
}