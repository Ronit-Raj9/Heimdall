import { useTranslation } from "react-i18next";
import "@/lib/types/evaluateI18n";

/** Step 5 stub. Launch: POST /evaluations, then go to /evaluations/$id (lane A13). Reads and writes the draft with useEvaluateDraft() from ./evaluateDraft. */
export function Step5Launch() {
	const { t } = useTranslation("evaluate");
	return (
		<div className="text-muted-foreground rounded-xl border border-dashed p-8 text-sm" data-testid="evaluate-step-5-placeholder">
			{t("placeholder")}
		</div>
	);
}

export default Step5Launch;
