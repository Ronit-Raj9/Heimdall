import { Info, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/inbox/views/uf7aI18n";

const DISMISS_KEY = "heimdall.clustersBanner.dismissed";

function readDismissed(): boolean {
	try {
		return globalThis.localStorage?.getItem(DISMISS_KEY) === "1";
	} catch {
		return false;
	}
}

/**
 * The standing caveat on the clusters screen: a cluster groups runs that failed the same recorded
 * way, not a cause. A muted one-line note under the counts, not a warning; it can be hidden (M7-39).
 */
export function ClustersBanner() {
	const { t } = useTranslation("bench");
	const { t: f } = useTranslation("uf7a");
	const [hidden, setHidden] = useState(readDismissed);
	if (hidden) return null;
	return (
		<p role="note" data-testid="clusters-banner" className="text-muted-foreground flex items-start gap-2 text-sm">
			<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span className="min-w-0 flex-1">
				<span className="text-foreground">{t("clusters.banner")}</span> {t("clusters.bannerHint")}
			</span>
			<button
				type="button"
				className="hover:bg-accent focus-visible:ring-ring/50 inline-flex size-6 shrink-0 items-center justify-center rounded-sm focus-visible:ring-[3px] focus-visible:outline-none"
				aria-label={f("clusters.hideNote")}
				onClick={() => {
					setHidden(true);
					try {
						globalThis.localStorage?.setItem(DISMISS_KEY, "1");
					} catch {
						// Storage blocked: hidden for this visit only.
					}
				}}
				data-testid="clusters-banner-hide"
			>
				<X className="size-3.5" aria-hidden="true" />
			</button>
		</p>
	);
}