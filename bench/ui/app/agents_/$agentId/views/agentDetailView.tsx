import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import ChecksTab from "./checksTab";
import ConfigTab from "./configTab";
import ToolsTab from "./toolsTab";

/** One agent (/agents/$agentId?tab=): Config (form and YAML), Checks and Tools. */
export default function AgentDetailView() {
	const { t } = useTranslation("agentBuilder");
	const { agentId } = useParams({ from: "/agents_/$agentId" });
	const tabs: RouteTab[] = [
		{ id: "config", label: t("detail.tabs.config") },
		{ id: "checks", label: t("detail.tabs.checks") },
		{ id: "tools", label: t("detail.tabs.tools") },
	];
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="agent-detail-screen">
			<PageHeader
				title={agentId}
				subtitle={t("detail.subtitle", { id: agentId })}
				breadcrumbs={[{ label: t("detail.back"), to: "/agents" }, { label: agentId }]}
			/>
			<RouteTabs tabs={tabs} defaultTab="config" label={t("detail.tabs.label")} testId="agent-detail-tabs">
				{(active) =>
					active === "checks" ? (
						<ChecksTab agentId={agentId} />
					) : active === "tools" ? (
						<ToolsTab agentId={agentId} />
					) : (
						<ConfigTab agentId={agentId} />
					)
				}
			</RouteTabs>
		</section>
	);
}
