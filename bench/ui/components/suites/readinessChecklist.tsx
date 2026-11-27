import { JobRunButton } from "@/components/jobs/jobRunButton";
import { benchApi } from "@/lib/store/apis/benchApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { SuiteSummary } from "@/lib/types/bench";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck, CircleDashed, type LucideIcon } from "lucide-react";
import { useCallback, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { checklistSteps, nextStep, type ChecklistStepKey, type ChecklistStepState } from "./readinessChecklist.utils";
import { TONE_CLASS, type Tone } from "./suites.utils";
import "./uf6aI18n";

/** The id the header's "Make it ready" button scrolls to (M6-13). */
export const READINESS_CHECKLIST_ID = "suite-readiness-checklist";

const STATE_TONE: Record<ChecklistStepState, Tone> = { todo: "neutral", done: "good", redo: "warn", partly: "neutral" };
const STATE_ICON: Record<ChecklistStepState, LucideIcon> = {
	todo: CircleDashed,
	done: CircleCheck,
	redo: CircleAlert,
	partly: CircleDashed,
};

export interface ReadinessChecklistProps {
	suite: SuiteSummary;
	/** Members only; a viewer sees the steps and their state with no buttons. */
	writable: boolean;
	/** Hosted mode: the readiness check runs on a worker (other confirm text). */
	hosted?: boolean;
	/** Replaces the step 3 button (Build passes its own with the task scope and judge consent). */
	admitControl?: ReactNode;
	/** Called when a freeze or readiness job ends. */
	onDone?: () => void;
	testId?: string;
}

/**
 * M6-12: the one place that says what "not ready" needs. Three numbered steps (Check files, Freeze
 * this version, Run the readiness check), each with its state and its own button. Used at the top of
 * the Tasks tab and in Build section 4, so the readiness check has one name and one button per view.
 */
export function ReadinessChecklist({
	suite,
	writable,
	hosted = false,
	admitControl,
	onDone,
	testId = "suite-readiness-checklist",
}: ReadinessChecklistProps) {
	const { t } = useTranslation("uf6a");
	const { t: tops } = useTranslation("suiteops");
	const dispatch = useAppDispatch();
	const refresh = useCallback(() => {
		dispatch(
			benchApi.util.invalidateTags([
				{ type: "BenchSuites", id: "LIST" },
				{ type: "BenchSuites", id: `${suite.id}/health` },
			]),
		);
		onDone?.();
	}, [dispatch, suite.id, onDone]);
	const steps = checklistSteps(suite);
	const next = nextStep(steps);
	const button = (key: ChecklistStepKey): ReactNode => {
		if (!writable) return null;
		const variant = key === next ? "default" : "outline";
		switch (key) {
			case "lint":
				return <JobRunButton kind={SUITE_OPS_KINDS.lint} params={{ suite: suite.id }} label={t("checklist.steps.lint")} variant={variant} />;
			case "freeze":
				return (
					<JobRunButton
						kind={SUITE_OPS_KINDS.lock}
						params={{ suite: suite.id }}
						label={t("checklist.steps.freeze")}
						variant={variant}
						confirm={tops("checks.lockConfirm", { suite: suite.id })}
						onDone={refresh}
					/>
				);
			case "admit":
				return (
					admitControl ?? (
						<JobRunButton
							kind={SUITE_OPS_KINDS.admit}
							params={{ suite: suite.id }}
							label={t("checklist.steps.admit")}
							variant={variant}
							confirm={
								hosted ? tops("checks.hostedAdmitConfirm", { suite: suite.id }) : tops("checks.admitConfirm", { suite: suite.id })
							}
							onDone={refresh}
						/>
					)
				);
		}
	};
	return (
		<section
			id={READINESS_CHECKLIST_ID}
			className="bg-card flex min-w-0 scroll-mt-4 flex-col gap-3 rounded-xl border p-4"
			aria-labelledby={`${testId}-title`}
			data-testid={testId}
		>
			<div className="flex flex-col gap-0.5">
				<h2 id={`${testId}-title`} className="text-foreground text-base font-semibold tracking-tight">
					{t("checklist.title")}
				</h2>
				<p className="text-muted-foreground max-w-prose text-sm">{t("checklist.description")}</p>
			</div>
			<ol className="flex min-w-0 flex-col gap-3">
				{steps.map((step, i) => {
					const Icon = STATE_ICON[step.state];
					return (
						<li
							key={step.key}
							className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between"
							data-testid={`${testId}-step-${step.key}`}
							data-state={step.state}
						>
							<div className="flex min-w-0 flex-col gap-1">
								<span className="text-foreground text-sm font-medium">
									{i + 1}. {t(`checklist.steps.${step.key}`)}
								</span>
								<span className="text-muted-foreground text-xs">{t(`checklist.hints.${step.key}`)}</span>
								<span
									className={cn(
										"inline-flex w-fit items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium",
										TONE_CLASS[STATE_TONE[step.state]],
									)}
									data-tone={STATE_TONE[step.state]}
								>
									<Icon className="size-3 shrink-0" aria-hidden="true" />
									{step.key === "admit" && step.state === "partly"
										? t("checklist.state.partly", { ready: suite.admitted_count, total: suite.task_count })
										: t(`checklist.state.${step.state === "partly" ? "todo" : step.state}`)}
								</span>
							</div>
							<div className="shrink-0">{button(step.key)}</div>
						</li>
					);
				})}
			</ol>
			{hosted && writable && (
				<p className="text-muted-foreground text-xs" data-testid={`${testId}-hosted`}>
					{tops("checks.hostedAdmit", { suite: suite.id })}
				</p>
			)}
		</section>
	);
}
