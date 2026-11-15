import { Info } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The standing caveat on the clusters screen: a cluster groups runs that failed the same recorded way, not a cause. */
export function ClustersBanner() {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			data-testid="clusters-banner"
			className="border-chart-warning/50 bg-chart-warning/10 flex items-start gap-3 rounded-sm border px-4 py-3 text-sm"
		>
			<Info className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex flex-col gap-0.5">
				<p className="text-foreground font-medium">{t("clusters.banner")}</p>
				<p className="text-muted-foreground text-xs">{t("clusters.bannerHint")}</p>
			</div>
		</div>
	);
}