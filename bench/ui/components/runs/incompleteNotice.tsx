import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Shown whenever GET /runs/{id}/events says complete: false. The lane is still shown, never hidden. */
export function IncompleteNotice({ arm }: { arm: string }) {
	const { t } = useTranslation("bench");
	return (
		<div
			role="status"
			data-testid={`gateway-incomplete-${arm}`}
			className="border-chart-warning/60 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
		>
			<AlertTriangle className="text-chart-warning-ink mt-px size-3.5 shrink-0" aria-hidden="true" />
			<span>{t("runs.incomplete", { arm })}</span>
		</div>
	);
}