import CanariesView from "@/app/canaries/views/canariesView";
import SchedulesView from "@/app/schedules/views/schedulesView";
import LiveMonitorsView from "./views/liveMonitorsView";
import { PageHeader, RouteTabs } from "@/components/shell";
import { EmbeddedPage } from "@/components/shell/embedded";
import "@/lib/types/hubsI18n";
import "@/components/canaries/uf7bI18n";
import { useTranslation } from "react-i18next";

// Composition only. Checks: Drift monitors (the old /canaries, ?name= opens one), Repeat runs (the
// old /schedules) and Live monitors (Z22, ?monitor= opens one) as tabs, ?tab= in the URL. The h1 is
// the page name on every tab (M7-55); each tab's own header is a section heading under it.
export default function ScheduledChecksPage() {
	const { t } = useTranslation("hubs");
	const { t: m } = useTranslation("uf7b");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-4" data-testid="scheduled-checks-page">
			<PageHeader title={m("checks.title")} subtitle={m("checks.subtitle")} testId="scheduled-checks-header" />
			<RouteTabs
				tabs={[
					{ id: "drift", label: m("checks.tab.drift") },
					{ id: "schedules", label: m("checks.tab.schedules") },
					{ id: "live", label: m("checks.tab.live") },
				]}
				defaultTab="drift"
				label={t("scheduled.label")}
				testId="scheduled-checks-tabs"
				className="w-full"
			>
				{(active) => (
					<EmbeddedPage>
						{active === "schedules" ? <SchedulesView /> : active === "live" ? <LiveMonitorsView /> : <CanariesView />}
					</EmbeddedPage>
				)}
			</RouteTabs>
		</div>
	);
}
