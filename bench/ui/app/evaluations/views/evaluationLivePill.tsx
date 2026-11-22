import "@/app/evaluations_/$evalId/views/fw08I18n";
import { StatusPill } from "@/components/ui/statusPill";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { useTranslation } from "react-i18next";
import { displayStatus } from "./evaluation.utils";
import { EvaluationStatusPill } from "./evaluationWords";

/** The evaluation's status pill, "Paused" while a running evaluation is paused (run controls). */
export function EvaluationLivePill({ evaluation, testId }: { evaluation: Evaluation; testId?: string }) {
	const { t } = useTranslation("fw08");
	if (displayStatus(evaluation) === "paused") {
		return (
			<StatusPill tone="warn" testId={testId} data-status="paused">
				{t("paused")}
			</StatusPill>
		);
	}
	return <EvaluationStatusPill status={evaluation.status} testId={testId} />;
}