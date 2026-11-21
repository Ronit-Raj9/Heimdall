import PoliciesView from "@/app/policies/views/policiesView";
import ReviewView from "@/app/review/views/reviewView";
import { RouteTabs } from "@/components/shell";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";
import ReleasesView from "./views/releasesView";

// Composition only. Releases: the release history (default), Needs review (the old /review)
// and Rules (the old /policies) as tabs, ?tab= in the URL.
export default function ReleasesPage() {
	const { t } = useTranslation("hubs");
	return (
		<RouteTabs
			tabs={[
				{ id: "history", label: t("releases.history") },
				{ id: "review", label: t("releases.review") },
				{ id: "rules", label: t("releases.rules") },
			]}
			defaultTab="history"
			label={t("releases.label")}
			testId="releases-tabs"
			className="mx-auto w-full max-w-[1400px]"
		>
			{(active) => (active === "review" ? <ReviewView /> : active === "rules" ? <PoliciesView /> : <ReleasesView />)}
		</RouteTabs>
	);
}
