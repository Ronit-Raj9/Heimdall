import { useTranslation } from "react-i18next";
import "@/lib/types/evaluateI18n";

/** Step 4 stub. Model, trials, faults and live estimate (lane A13). Reads and writes the draft with useEvaluateDraft() from ./evaluateDraft. */
export function Step4Setup() {
	const { t } = useTranslation("evaluate");
	return (
		<div className="text-muted-foreground rounded-xl border border-dashed p-8 text-sm" data-testid="evaluate-step-4-placeholder">
			{t("placeholder")}
		</div>
	);
}

export default Step4Setup;
