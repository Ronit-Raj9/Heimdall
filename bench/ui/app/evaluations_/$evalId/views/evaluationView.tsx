import { EvaluationRunActions } from "@/components/experiments/runControls"; // run control: pause, resume, rerun failures
import { EvaluationStatusPill, ModeLabel } from "@/app/evaluations/views/evaluationWords";
import { EVALUATION_POLL_MS, armAgentLabel, formatCreated, isActiveStatus, splitChildren } from "@/app/evaluations/views/evaluation.utils";
import { useConfirm } from "@/components/platform/confirmDialog";
import { EvaluationReportCard } from "@/components/reportcard/evaluationReportCard";
import { GateResultCard, PromoteEvaluation, RepeatEvaluation } from "@/components/releases/promoteEvaluation";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { ShareButton } from "@/components/share/shareDialog";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import {
	useCancelEvaluationMutation,
	useGetEvaluationQuery,
	type Evaluation,
	type EvaluationArm,
	type EvaluationChild,
} from "@/lib/store/apis/evaluationsApi";
import { Link, useNavigate } from "@tanstack/react-router";
import { DeleteAction } from "@/components/platform/deleteAction";
import { useDeleteEvaluationMutation } from "@/lib/store/apis/dataOpsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** /evaluations/$evalId: live progress per benchmark, then the report card. */
export default function EvaluationView({ evalId }: { evalId: string }) {
	const { t } = useTranslation("evaluations");
	const first = useGetEvaluationQuery(evalId);
	const active = first.data ? isActiveStatus(first.data.status) : false;
	// Poll every 3 s while the evaluation is still moving; RTK Query shares the cache entry.
	useGetEvaluationQuery(evalId, { pollingInterval: active ? EVALUATION_POLL_MS : 0, skip: !active });
	if (first.isLoading) return <BlockSkeleton rows={6} testId="evaluation-loading" />;
	if (first.error || !first.data) {
		return (
			<LoadError
				title={t("detail.loadError", { id: evalId })}
				error={first.error}
				onRetry={() => void first.refetch()}
				testId="evaluation-error"
			/>
		);
	}
	return <EvaluationBody evaluation={first.data} />;
}

/** The loaded evaluation. Exported for tests. */
export function EvaluationBody({ evaluation }: { evaluation: Evaluation }) {
	const { t, i18n } = useTranslation("evaluations");
	const { run, skipped } = splitChildren(evaluation.children);
	const active = isActiveStatus(evaluation.status);
	const spec = evaluation.spec;
	const faults = (spec.faults ?? []).filter((f) => f !== "clean");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="evaluation-screen" data-status={evaluation.status}>
			<PageHeader
				title={spec.name || evaluation.id}
				subtitle={t("detail.subtitle")}
				breadcrumbs={[{ label: t("detail.crumb"), to: "/evaluations" }, { label: spec.name || evaluation.id }]}
				meta={<EvaluationStatusPill status={evaluation.status} testId="evaluation-status" />}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						<ShareButton kind="evaluation" target={evaluation.id} evaluationId={evaluation.id} />
						<EvaluationRunActions evaluation={evaluation} />
						{active ? <CancelButton id={evaluation.id} /> : <RepeatEvaluation evaluationId={evaluation.id} />}
						<DeleteEvaluationAction id={evaluation.id} active={active} />
					</div>
				}
			/>
			<section className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="evaluation-header">
				<div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm">
					<span className="font-medium" data-testid="evaluation-mode">
						<ModeLabel mode={spec.mode} />
					</span>
					<span className="text-muted-foreground">{t("detail.setup", { tasks: spec.tasks_per_benchmark, trials: spec.trials })}</span>
					<span className="text-muted-foreground">
						{faults.length > 0
							? t("detail.faults", { faults: faults.map((f) => t(`fault.${f}` as never, { defaultValue: f })).join(", ") })
							: t("detail.noFaults")}
					</span>
					<span className="text-muted-foreground ml-auto text-xs tabular-nums">{formatCreated(evaluation.created_at, i18n.language)}</span>
				</div>
				<div className="grid gap-3 sm:grid-cols-2">
					{(spec.arms ?? []).map((arm) => (
						<ArmCard key={arm.label} arm={arm} single={(spec.arms ?? []).length < 2} />
					))}
				</div>
			</section>
			<Section
				title={t("detail.progressTitle")}
				description={t(spec.mode === "score" ? "detail.progressDescriptionScore" : "detail.progressDescription")}
				testId="evaluation-progress"
			>
				{run.length === 0 ? (
					<EmptyNote testId="evaluation-no-benchmarks">{t("detail.noBenchmarks")}</EmptyNote>
				) : (
					<ul className="flex flex-col divide-y rounded-xl border">
						{run.map((child) => (
							<ChildRow key={child.benchmark} child={child} score={spec.mode === "score"} />
						))}
					</ul>
				)}
			</Section>
			{skipped.length > 0 && (
				<Section title={t("detail.skippedTitle")} description={t("detail.skippedDescription")} testId="evaluation-skipped">
					<ul className="flex flex-col gap-1 text-sm">
						{skipped.map((child) => (
							<li key={child.benchmark} data-testid={`evaluation-skipped-${child.benchmark}`}>
								<span className="font-medium">{child.benchmark}</span>
								{child.skipped && <span className="text-muted-foreground">: {child.skipped}</span>}
							</li>
						))}
					</ul>
				</Section>
			)}
			{evaluation.status === "done" ? (
				<>
					{evaluation.gate_result && <GateResultCard gate={evaluation.gate_result} />}
					<EvaluationReportCard evaluationId={evaluation.id} />
					{(spec.mode === "versions" || spec.mode === "score") && <PromoteEvaluation evaluationId={evaluation.id} />}
				</>
			) : (
				active && (
					<p className="text-muted-foreground text-sm" data-testid="evaluation-report-waiting">
						{t("detail.reportWaiting")}
					</p>
				)
			)}
		</div>
	);
}

function ArmCard({ arm, single }: { arm: EvaluationArm; single: boolean }) {
	const { t } = useTranslation("evaluations");
	return (
		<div className="bg-muted/20 flex flex-col gap-1 rounded-lg border p-3 text-sm" data-testid={`evaluation-arm-${arm.label}`}>
			{!single && <span className="text-muted-foreground text-xs font-medium">{t("detail.armTitle", { label: arm.label })}</span>}
			<dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
				<dt className="text-muted-foreground">{t("detail.armAgent")}</dt>
				<dd className="font-medium">{arm.agent}</dd>
				<dt className="text-muted-foreground">{t("detail.armVersion")}</dt>
				<dd data-testid={`evaluation-arm-version-${arm.label}`}>
					{arm.version
						? arm.latest_at_launch
							? t("detail.versionLatestAtLaunch", { n: arm.version })
							: t("detail.versionN", { n: arm.version })
						: t("detail.latestVersion")}
				</dd>
				<dt className="text-muted-foreground">{t("detail.armModel")}</dt>
				<dd className="break-all">{!arm.model ? t("detail.ownModel", { defaultValue: "The agent's own model" }) : arm.provider ? `${arm.provider}/${arm.model}` : arm.model}</dd>
			</dl>
			<span className="sr-only">{armAgentLabel(arm)}</span>
		</div>
	);
}

/** One benchmark: status, runs done of total (from its experiment, polled while running), links. */
export function ChildRow({ child, score = false }: { child: EvaluationChild; score?: boolean }) {
	const { t } = useTranslation("evaluations");
	const active = isActiveStatus(child.status);
	const exp = useGetExperimentQuery(child.experiment_id, { pollingInterval: active ? EVALUATION_POLL_MS : 0 });
	const slots = exp.data?.slots;
	return (
		<li className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm" data-testid={`evaluation-child-${child.benchmark}`}>
			<span className="min-w-40 font-medium">{child.benchmark}</span>
			<EvaluationStatusPill status={child.status} testId={`evaluation-child-status-${child.benchmark}`} />
			<span className="text-muted-foreground tabular-nums" data-testid={`evaluation-child-runs-${child.benchmark}`}>
				{slots ? t("detail.runs", { done: slots.done, total: slots.total }) : t("detail.runsUnknown")}
			</span>
			<span className="ml-auto flex gap-3">
				<Link
					to="/experiments/$expId"
					params={{ expId: child.experiment_id }}
					className="text-primary hover:underline"
					data-testid={`evaluation-child-open-${child.benchmark}`}
				>
					{t(score ? "detail.openResults" : "detail.openComparison")}
				</Link>
				<Link
					to="/experiments/$expId"
					params={{ expId: child.experiment_id }}
					search={{ tab: "runs" } as never}
					className="text-primary hover:underline"
					data-testid={`evaluation-child-live-${child.benchmark}`}
				>
					{t("detail.watchRuns")}
				</Link>
			</span>
		</li>
	);
}

/** Delete (admin): confirm, delete with its benchmark comparisons, back to the list. Disabled while queued or running. */
function DeleteEvaluationAction({ id, active }: { id: string; active: boolean }) {
	const { t } = useTranslation("evaluations");
	const navigate = useNavigate();
	const [remove] = useDeleteEvaluationMutation();
	return (
		<DeleteAction
			label={t("detail.delete")}
			confirmTitle={t("detail.deleteConfirmTitle")}
			confirmBody={t("detail.deleteConfirm")}
			confirmLabel={t("detail.deleteConfirmOk")}
			cancelLabel={t("detail.deleteKeep")}
			busyReason={active ? t("detail.deleteBusy") : undefined}
			adminOnly={t("detail.deleteAdminOnly")}
			errorText={(error) => t("detail.deleteError", { error })}
			onDelete={() => remove(id).unwrap()}
			onDeleted={() => void navigate({ to: "/evaluations" })}
			testId="evaluation-delete"
		/>
	);
}

function CancelButton({ id }: { id: string }) {
	const { t } = useTranslation("evaluations");
	const confirm = useConfirm();
	const [cancel, state] = useCancelEvaluationMutation();
	const [error, setError] = useState<string | null>(null);
	return (
		<span className="flex items-center gap-2">
			{error && <span className="text-chart-error-ink text-xs">{t("detail.cancelError", { error })}</span>}
			<Button
				size="sm"
				variant="outline"
				disabled={state.isLoading}
				data-testid="evaluation-cancel"
				onClick={async () => {
					const ok = await confirm(t("detail.cancelConfirm"), {
						title: t("detail.cancelConfirmTitle"),
						confirmLabel: t("detail.cancelConfirmOk"),
						cancelLabel: t("detail.cancelKeep"),
						destructive: true,
					});
					if (!ok) return;
					setError(null);
					try {
						await cancel(id).unwrap();
					} catch (e) {
						const msg = (e as { data?: { error?: { message?: string } } })?.data?.error?.message;
						setError(msg ?? String((e as { status?: unknown })?.status ?? "error"));
					}
				}}
			>
				{t("detail.cancel")}
			</Button>
		</span>
	);
}