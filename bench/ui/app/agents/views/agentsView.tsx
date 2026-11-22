import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { useGetAgentsQuery } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { Plug, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import AgentDiffTab from "./agentDiffTab";
import { AgentConfigsTab } from "./agentConfigsTab";
import { FrameworksTab } from "./frameworksTab";

/**
 * Agents: /agents?tab=&agent=. Configs (default) lists every agents/*.yaml config and opens one
 * with ?agent=; Diff compares two versions of an agent (the old /agents/diff); Frameworks sets the
 * configs side by side by how they are built and what evidence they report.
 */
export default function AgentsView() {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("agentsUx");
	const { t: tc } = useTranslation("connect"); // lane Z01: the Connect box
	const query = useGetAgentsQuery();
	const data = query.data;
	const tabs: RouteTab[] = [
		{ id: "configs", label: t("agents.tabs.configs"), badge: data ? data.agents.length : undefined },
		{ id: "diff", label: t("agents.tabs.diff") },
		{ id: "frameworks", label: t("agents.tabs.frameworks") },
	];
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="agents-screen">
			<PageHeader
				title={t("agents.title")}
				subtitle={tu("subtitle")}
				actions={
					<div className="flex items-center gap-2">
						<Button asChild size="sm" variant="outline" data-testid="agents-connect">
							<Link to="/connect">
								<Plug aria-hidden="true" />
								{tc("title")}
							</Link>
						</Button>
						<Button asChild size="sm" data-testid="agents-new">
							<Link to="/agents/new">
								<Plus aria-hidden="true" />
								{tb("new.title")}
							</Link>
						</Button>
					</div>
				}
			/>
			<RouteTabs tabs={tabs} defaultTab="configs" label={t("agents.tabs.label")} testId="agents-tabs">
				{(active) =>
					active === "diff" ? (
						<AgentDiffTab />
					) : active === "frameworks" ? (
						<FrameworksTab query={query} />
					) : (
						<AgentConfigsTab query={query} />
					)
				}
			</RouteTabs>
		</section>
	);
}