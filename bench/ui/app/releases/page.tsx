import PoliciesView from "@/app/policies/views/policiesView";
import ReviewView from "@/app/review/views/reviewView";
import { PageHeader, RouteTabs } from "@/components/shell";
import { EmbeddedPage } from "@/components/shell/embedded";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";
import ReleasesView from "./views/releasesView";

// Composition only. Releases: the release history (default), Needs review (the old /review)
// and Rules (the old /policies) as tabs, ?tab= in the URL. The h1 sits above the tabs on every tab
// (Q-71, as Checks does); each tab's own header is a section heading under it.
export default function ReleasesPage() {
	const { t } = useTranslation("hubs");
	const { t: tu } = useTranslation("u14");
	return (
		<div className="mx-auto flex w-full max-w-[1400px] min-w-0 flex-col gap-4" data-testid="releases-page">
			<PageHeader title={tu("releases.title")} subtitle={tu("releases.subtitle")} testId="releases-header" />
			<RouteTabs
				tabs={[
					{ id: "history", label: t("releases.history") },
					{ id: "review", label: t("releases.review") },
					{ id: "rules", label: t("releases.rules") },
				]}
				defaultTab="history"
				label={t("releases.label")}
				testId="releases-tabs"
				className="w-full"
			>
				{(active) => (
					<EmbeddedPage>{active === "review" ? <ReviewView /> : active === "rules" ? <PoliciesView /> : <ReleasesView />}</EmbeddedPage>
				)}
			</RouteTabs>
		</div>
	);
}
