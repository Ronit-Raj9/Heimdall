import { PageHeader } from "@/components/shell";
import { useArchiveItems } from "@/components/ui/archiveAction";
import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { StatusPill } from "@/components/ui/statusPill";
import { ImprovementDetailBody } from "@/components/improvements/improvementDetail";
import { statusTone } from "@/components/improvements/improve.utils";
import { ContinueDialog } from "@/components/improvements/continueDialog";
import "@/components/improvements/w45impI18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	useApproveImprovementRoundMutation,
	useGetImprovementQuery,
	useResumeImprovementMutation,
	useStopImprovementMutation,
} from "@/lib/store/apis/improveApi";
import { useEffect, useState } from "react";
import type { ImprovementStatus } from "@/lib/types/improve";
import { improvementPollMs } from "@/components/improvements/improvementPoll";
import { useTranslation } from "react-i18next";

/** /improvements/$improvementId: the improvement report, polled while the run is live. */
export default function ImprovementView({ id }: { id: string }) {
	const { t } = useTranslation("improve");
	const confirm = useConfirm();
	// Poll only while the run can still change (M1-62); the status comes from the last answer.
	const [status, setStatus] = useState<ImprovementStatus | undefined>(undefined);
	const q = useGetImprovementQuery(id, { pollingInterval: improvementPollMs(status), skipPollingIfUnfocused: true });
	useEffect(() => setStatus(q.data?.status), [q.data?.status]);
	const [approve, approveState] = useApproveImprovementRoundMutation();
	const [stop, stopState] = useStopImprovementMutation();
	const [resume, resumeState] = useResumeImprovementMutation();
	const { t: tw } = useTranslation("w45imp");
	// Resume and Continue spend the organization's keys, so only admins see them (policy org(admin)).
	const admin = canUse(useSession(), "admin");
	const [continuing, setContinuing] = useState(false);
	const d = q.data;
	// Phase D decision 6: Archive in the More menu.
	const archive = useArchiveItems({ kind: "improvement", id, name: d?.name ?? id, archivedAt: (d as { archived_at?: string | null } | undefined)?.archived_at, testId: "improvement" });
	// Stop, resume and approve can fail after the click; show why below the header (M1-44).
	const [actionError, setActionError] = useState<unknown>(null);
	const name = d?.name ?? id;
	const onStop = async () => {
		if (!(await confirm(t("detail.stopConfirm")))) return;
		setActionError(null);
		try {
			await stop(id).unwrap();
			notify({ text: t("stopped", { ns: "toast", name }) });
		} catch (e) {
			setActionError(e);
		}
	};
	const onResume = async () => {
		setActionError(null);
		try {
			await resume(id).unwrap();
			notify({ text: t("started", { ns: "toast", name }) });
		} catch (e) {
			setActionError(e);
		}
	};
	const onApprove = async (round: number, reason: string) => {
		setActionError(null);
		try {
			await approve({ id, round, reason }).unwrap();
		} catch (e) {
			setActionError(e);
		}
	};
	const live = d && (d.status === "running" || d.status === "awaiting_approval" || d.status === "planned");
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="improvement-screen">
			<PageHeader
				title={d?.name ?? id}
				subtitle={d ? `${d.agent} on ${d.suite}` : undefined}
				breadcrumbs={[{ label: t("detail.back"), to: "/improvements" }]}
				meta={
					d && (
						<StatusPill tone={statusTone(d.status)} pulse={d.running}>
							{t(`status.${d.status}`)}
						</StatusPill>
					)
				}
				more={d ? archive.items : undefined}
				actions={
					d?.can_resume && admin ? (
						<PaidButton
							size="sm"
							verb={tw("resume.button")}
							capUsd={null}
							confirm="always"
							confirmText={tw("resume.confirm")}
							estimateAt="title"
							isLoading={resumeState.isLoading}
							onConfirm={onResume}
							testId="improvement-resume"
						/>
					) : live ? (
						<Button
							size="sm"
							variant="outline"
							disabled={stopState.isLoading}
							onClick={() => void onStop()}
							data-testid="improvement-stop"
						>
							{t("detail.stop")}
						</Button>
					) : undefined
				}
			/>
			<InlineError error={actionError} testId="improvement-action-error" />
			{archive.alert}
			{q.isLoading ? (
				<BlockSkeleton rows={6} testId="improvement-loading" />
			) : q.error || !d ? (
				<LoadError
					title={t("detail.loadError")}
					error={q.error}
					onRetry={() => void q.refetch()}
					testId="improvement-error"
					notFound={{ text: t("detail.notFound", { id }), backTo: "/improvements", backLabel: t("detail.notFoundBack") }}
				/>
			) : (
				<ImprovementDetailBody
					data={d}
					onContinue={admin ? () => setContinuing(true) : undefined}
					approving={approveState.isLoading}
					onApprove={(round, reason) => void onApprove(round, reason)}
				/>
			)}
			{continuing && <ContinueDialog id={id} onClose={() => setContinuing(false)} />}
		</section>
	);
}