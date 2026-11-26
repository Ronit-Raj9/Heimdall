import "@/app/validity/views/w449I18n";

import { Button } from "@/components/ui/button";
import {
	type ExperimentValidity,
	type ValidityEntry,
	useGetExperimentValidityQuery,
	useMarkValiditySeenMutation,
} from "@/lib/store/apis/validityApi";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The re-scored-on date, e.g. "23 Nov". */
export function flipDate(at: string): string {
	const d = new Date(at);
	if (Number.isNaN(d.getTime())) return at;
	return d.toLocaleDateString("en-GB", { day: "numeric", month: "short", timeZone: "UTC" });
}

/** The newest flipped entry of a comparison, if any. */
export function newestFlip(v: ExperimentValidity | undefined): ValidityEntry | undefined {
	return v?.entries.find((e) => e.flipped);
}

interface FlipBannerViewProps {
	validity: ExperimentValidity | undefined;
	onSeen?: (id: string) => void;
}

/** W4-49: tells the reader a re-score changed this comparison's result, or that a check it used was updated. */
export function FlipBannerView({ validity, onSeen }: FlipBannerViewProps) {
	const { t } = useTranslation("w449");
	const { t: tg } = useTranslation("glossary");
	const word = (d: string) => tg(`terms.${d}.label`, { defaultValue: d });
	const flip = newestFlip(validity);
	if (flip) {
		return (
			<div className="border-border bg-muted/50 flex flex-wrap items-center gap-3 rounded-md border p-3 text-sm" data-testid="flip-banner">
				<span className="min-w-0 flex-1">
					{t("banner.changed", {
						date: flipDate(flip.at),
						before: word(flip.before.decision),
						after: word(flip.after.decision),
						note: flip.note ?? "",
						interpolation: { escapeValue: false },
					}).trim()}
				</span>
				<Link to="/validity" className="text-primary font-medium hover:underline">
					{t("banner.seeWhat")}
				</Link>
				<Button size="sm" variant="outline" onClick={() => onSeen?.(flip.id)} data-testid="flip-banner-seen">
					{t("banner.gotIt")}
				</Button>
			</div>
		);
	}
	if (validity?.stale) {
		return (
			<div className="border-border bg-muted/50 rounded-md border p-3 text-sm" data-testid="flip-banner-stale">
				{t("banner.stale")}
			</div>
		);
	}
	return null;
}

/** Reads the comparison's ledger and shows the banner until the reader dismisses it. */
export function FlipBanner({ expId }: { expId: string }) {
	const { data } = useGetExperimentValidityQuery(expId);
	const [markSeen] = useMarkValiditySeenMutation();
	return <FlipBannerView validity={data} onSeen={(id) => void markSeen(id)} />;
}