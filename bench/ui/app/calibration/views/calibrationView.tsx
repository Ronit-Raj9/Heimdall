import { judgedSuites } from "@/components/calibration/calibration.utils";
import { CalibrationCard, LabelForm, LabelsTable } from "@/components/calibration/calibrationPanel";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import { useAddCalibrationLabelMutation, useGetCalibrationLabelsQuery, useGetCalibrationQuery } from "@/lib/store/apis/calibrationApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * W8 judge calibration: /calibration?suite=. For a judged suite: the calibration card (state,
 * agreement, kappa, false-positive bound, set size, date), the labelling form (write token, live
 * mode only) and every label. Running the judge stays on the command line, behind a forecast.
 */
export default function CalibrationView() {
	const { t } = useTranslation("bench");
	const suites = useGetSuitesQuery();
	const judged = judgedSuites(suites.data?.suites ?? []);
	const [picked, setPicked] = useQueryState("suite", parseAsString);
	const suiteId = picked ?? judged[0]?.id ?? null;
	const status = useGetCalibrationQuery(suiteId ?? skipToken);
	const labels = useGetCalibrationLabelsQuery(suiteId ?? skipToken);
	const [addLabel, added] = useAddCalibrationLabelMutation();
	const [formKey, setFormKey] = useState(0);
	const suite = judged.find((s) => s.id === suiteId);
	const submitError = added.error
		? String((added.error as { data?: { error?: { message?: string } } }).data?.error?.message ?? t("calibration.loadError"))
		: undefined;

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="calibration-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("calibration.title")}</h1>
					<ModeBadge mode={status.data?.mode ?? suites.data?.mode} isLoading={suites.isLoading} isError={suites.isError && !suites.data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("calibration.subtitle")}</p>
			</header>
			{suites.isLoading ? (
				<BlockSkeleton rows={4} testId="calibration-loading" />
			) : suites.error || !suites.data ? (
				<LoadError
					title={t("calibration.loadError")}
					error={suites.error}
					onRetry={() => void suites.refetch()}
					testId="calibration-error"
				/>
			) : judged.length === 0 ? (
				<EmptyNote testId="calibration-no-judged">{t("calibration.noJudgedSuites")}</EmptyNote>
			) : (
				<>
					<label className="flex max-w-sm flex-col gap-1 text-sm">
						{t("calibration.pickSuite")}
						<select
							className="border-input bg-background rounded-md border px-2 py-1.5 text-sm"
							value={suiteId ?? ""}
							onChange={(e) => void setPicked(e.target.value)}
							data-testid="calibration-suite"
						>
							{judged.map((s) => (
								<option key={s.id} value={s.id}>
									{s.id}
								</option>
							))}
						</select>
					</label>
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
								key={`${suiteId}-${formKey}`}
								tasks={(suite?.tasks ?? []).map((task) => task.id)}
								mode={status.data.mode}
								submitting={added.isLoading}
								submitError={submitError}
								onSubmit={(body) =>
									void addLabel({ suiteId: suiteId!, body })
										.unwrap()
										.then(() => setFormKey((k) => k + 1))
										.catch(() => {})
								}
							/>
						</div>
					)}
					<Section title={t("calibration.labelsTitle")} testId="calibration-labels-section">
						{labels.data ? <LabelsTable items={labels.data.items} /> : <BlockSkeleton rows={3} testId="calibration-labels-loading" />}
					</Section>
				</>
			)}
		</section>
	);
}