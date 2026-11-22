import CanariesView from "@/app/canaries/views/canariesView";
import SchedulesView from "@/app/schedules/views/schedulesView";
import LiveMonitorsView from "./views/liveMonitorsView";
import { RouteTabs } from "@/components/shell";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";

// Composition only. Scheduled checks: Drift checks (the old /canaries, ?name= opens one) and
// Schedules (the old /schedules) and Live monitors (Z22, ?monitor= opens one) as tabs, ?tab= in the URL.
export default function ScheduledChecksPage() {
	const { t } = useTranslation("hubs");
	return (
		<RouteTabs
			tabs={[
				{ id: "drift", label: t("scheduled.drift") },
				{ id: "schedules", label: t("scheduled.schedules") },
				{ id: "live", label: t("scheduled.live") },
			]}
			defaultTab="drift"
			label={t("scheduled.label")}
			testId="scheduled-checks-tabs"
			className="mx-auto w-full max-w-[1200px]"
		>
			{(active) => (active === "schedules" ? <SchedulesView /> : active === "live" ? <LiveMonitorsView /> : <CanariesView />)}
		</RouteTabs>
	);
}
