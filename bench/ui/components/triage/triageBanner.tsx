import { Info } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The standing caveat on the triage screen: every row is an observation over events, not a cause. */
export function TriageBanner() {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			data-testid="triage-banner"
			className="border-chart-warning/50 bg-chart-warning/10 flex items-start gap-3 rounded-sm border px-4 py-3 text-sm"
		>
			<Info className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex flex-col gap-0.5">
				<p className="text-foreground font-medium">{t("triage.banner")}</p>
			</div>
		</div>
	);
}