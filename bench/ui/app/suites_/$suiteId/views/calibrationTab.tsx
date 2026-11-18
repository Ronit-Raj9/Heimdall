import { CalibrationCard, LabelForm, LabelsTable } from "@/components/calibration/calibrationPanel";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { useAddCalibrationLabelMutation, useGetCalibrationLabelsQuery, useGetCalibrationQuery } from "@/lib/store/apis/calibrationApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * Calibration tab of a judged suite (W8, docs/judge-calibration.md; the old /calibration?suite=):
 * the calibration card (state, agreement, kappa, false-positive bound, set size, date), the
 * labelling form (write token, live mode only) and every label. Running the judge stays on the
 * command line, behind a forecast.
 */
export function SuiteCalibrationTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const status = useGetCalibrationQuery(suite.id);
	const labels = useGetCalibrationLabelsQuery(suite.id);
	const [addLabel, added] = useAddCalibrationLabelMutation();
	const [formKey, setFormKey] = useState(0);
	const submitError = added.error
		? String((added.error as { data?: { error?: { message?: string } } }).data?.error?.message ?? t("calibration.loadError"))
		: undefined;

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="calibration-screen">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("calibration.subtitle")}</p>
			{status.isLoading ? (
				<BlockSkeleton rows={6} testId="calibration-status-loading" />
			) : status.error || !status.data ? (
				<LoadError
					title={t("calibration.loadError")}
					error={status.error}
					onRetry={() => void status.refetch()}
					testId="calibration-status-error"
				/>
			) : (
				<div className="grid gap-6 lg:grid-cols-2">
					<CalibrationCard status={status.data} />
					<LabelForm
						key={`${suite.id}-${formKey}`}
						tasks={suite.tasks.map((task) => task.id)}
						mode={status.data.mode}
						submitting={added.isLoading}
						submitError={submitError}
						onSubmit={(body) =>
							void addLabel({ suiteId: suite.id, body })
								.unwrap()
								.then(() => setFormKey((k) => k + 1))
								.catch(() => {})
						}
					/>
				</div>
			)}
			<Section title={t("calibration.labelsTitle")} testId="calibration-labels-section">
				{labels.data ? (
					<LabelsTable items={labels.data.items} />
				) : labels.isError ? (
					<LoadError
						title={t("calibration.loadError")}
						error={labels.error}
						onRetry={() => void labels.refetch()}
						testId="calibration-labels-error"
					/>
				) : (
					<BlockSkeleton rows={3} testId="calibration-labels-loading" />
				)}
			</Section>
		</div>
	);
}