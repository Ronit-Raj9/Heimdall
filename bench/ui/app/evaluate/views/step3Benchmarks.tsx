import { useTranslation } from "react-i18next";
import "@/lib/types/evaluateI18n";

/** Step 3 stub. Benchmarks: packs, multi-select, incompatible greyed (lane A12). Reads and writes the draft with useEvaluateDraft() from ./evaluateDraft. */
export function Step3Benchmarks() {
	const { t } = useTranslation("evaluate");
	return (
		<div className="text-muted-foreground rounded-xl border border-dashed p-8 text-sm" data-testid="evaluate-step-3-placeholder">
			{t("placeholder")}
		</div>
	);
}

export default Step3Benchmarks;
