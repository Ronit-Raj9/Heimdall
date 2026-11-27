import { PageHeader } from "@/components/shell";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
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
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** /improvements/$improvementId: the improvement report, polled while the run is live. */
export default function ImprovementView({ id }: { id: string }) {
	const { t } = useTranslation("improve");
	const confirm = useConfirm();
	const q = useGetImprovementQuery(id, { pollingInterval: 5_000 });
	const [approve, approveState] = useApproveImprovementRoundMutation();
	const [stop, stopState] = useStopImprovementMutation();
	const [resume, resumeState] = useResumeImprovementMutation();
	const { t: tw } = useTranslation("w45imp");
	// Resume and Continue spend the organization's keys, so only admins see them (policy org(admin)).
	const admin = canUse(useSession(), "admin");
	const [continuing, setContinuing] = useState(false);
	const d = q.data;
	// Stop, resume and approve can fail after the click; show why below the header.
	const actionError = stopState.error ?? resumeState.error ?? approveState.error;
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
				actions={
					d?.can_resume && admin ? (
						<Button
							size="sm"
							disabled={resumeState.isLoading}
							onClick={async () => {
								if (await confirm(tw("resume.confirm"))) void resume(id);
							}}
							data-testid="improvement-resume"
						>
							{tw("resume.button")}
						</Button>
					) : live ? (
						<Button
							size="sm"
							variant="outline"
							disabled={stopState.isLoading}
							onClick={async () => {
								if (await confirm(t("detail.stopConfirm"))) void stop(id);
							}}
							data-testid="improvement-stop"
						>
							{t("detail.stop")}
						</Button>
					) : undefined
				}
			/>
			{actionError ? (
				<p role="alert" className="text-chart-error-ink text-sm" data-testid="improvement-action-error">
					{getBenchErrorMessage(actionError)}
				</p>
			) : null}
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
					onApprove={(round, reason) => void approve({ id, round, reason })}
				/>
			)}
			{continuing && <ContinueDialog id={id} onClose={() => setContinuing(false)} />}
		</section>
	);
}