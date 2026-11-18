import { CompliancePackLinks } from "@/components/integrity/compliancePack";
import { AwarenessSection, ExperimentMonitorsSection } from "@/components/integrity/integritySections";
import { VarianceCard } from "@/components/integrity/varianceCard";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { armOrder } from "@/components/verdict/verdict.utils";
import { useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useTranslation } from "react-i18next";
import { Section } from "./verdictTab";

/** The Integrity tab (eval integrity area, S2): the noise floor, eval awareness, monitor certifications and the compliance pack. */
export function IntegrityTab({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	// The same request as the Verdict tab (sealed grades), so it is read from the cache.
	const verdict = useGetVerdictQuery(expId);
	const arms = verdict.data ? armOrder(verdict.data) : [];
	return (
		<div className="flex flex-col gap-6" data-testid="integrity-screen">
			{verdict.isLoading ? (
				<BlockSkeleton rows={3} testId="integrity-variance-loading" />
			) : (
				verdict.data && (
					<div className="flex flex-col gap-2">
						<div className="flex justify-end">
							<GradeSourceTag
								source={verdict.data.grade_source}
								judged={Boolean(verdict.data.judge_calibration)}
								testId="integrity-source"
							/>
						</div>
						<VarianceCard verdict={verdict.data.verdict} />
					</div>
				)
			)}
			<Section title={t("integrity.awareness.title")} description={t("integrity.awareness.description")} testId="verdict-awareness">
				<AwarenessSection expId={expId} arms={arms} />
			</Section>
			<div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
				<Section title={t("integrity.monitors.title")} description={t("integrity.monitors.forExperiment")} testId="verdict-monitors">
					<ExperimentMonitorsSection expId={expId} />
				</Section>
				<Section title={t("integrity.compliance.title")} description={t("integrity.compliance.description")} testId="verdict-compliance">
					<CompliancePackLinks expId={expId} />
				</Section>
			</div>
		</div>
	);
}