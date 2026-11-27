import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Shown when the board holds synthetic runs or the server answers in synthetic mode: the numbers are not findings. */
export function LeaderboardSyntheticBanner() {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			data-testid="leaderboard-synthetic-banner"
			className="border-chart-warning/50 bg-chart-warning/10 flex items-start gap-3 rounded-sm border px-4 py-3 text-sm"
		>
			<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<p className="text-foreground font-medium">{t("leaderboard.banner")}</p>
		</div>
	);
}