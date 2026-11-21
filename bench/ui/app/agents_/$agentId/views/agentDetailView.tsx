import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Link, useParams } from "@tanstack/react-router";
import { useGetAgentCompatibilityQuery } from "@/lib/store/apis/evaluationsApi";
import { Cpu, Gauge, GitCompare, ListChecks, Pencil, Play } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AgentAvatar, CheckPill, adapterIcon, jobState, lastJobFor, useAdapterLabel } from "../../../agents/views/agentDisplay";
import { useAgentJobs } from "../../../agents/views/agentJobs";
import { AgentImageApproval } from "@/components/sharedpool/imageRequests";
import { SavedEndpointTest } from "../../new/views/connectForm";
import { useAgentSummary } from "./agentOps.shared";
import ChecksTab from "./checksTab";
import ConfigTab from "./configTab";
import ReportCardTab from "./reportCardTab";
import ToolsTab from "./toolsTab";
import VersionsTab from "./versionsTab";

/**
 * One agent (/agents/$agentId?tab=): a header with capabilities, Evaluate, Run, Check and Edit, then
 * Config, Checks, Tools, Versions and Report card.
 */
export default function AgentDetailView() {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("agentsUx");
	const { agentId } = useParams({ from: "/agents_/$agentId" });
	const { agent } = useAgentSummary(agentId);
	const jobs = useAgentJobs();
	const adapterLabel = useAdapterLabel();
	const compatibility = useGetAgentCompatibilityQuery(agentId);
	const capabilities = compatibility.data?.capabilities ?? [];
	const tabs: RouteTab[] = [
		{ id: "config", label: t("detail.tabs.config") },
		{ id: "checks", label: t("detail.tabs.checks") },
		{ id: "tools", label: t("detail.tabs.tools") },
		{ id: "versions", label: t("detail.tabs.versions") },
		{ id: "report-card", label: t("detail.tabs.reportCard") },
	];
	const Icon = agent ? adapterIcon(agent.adapter) : null;
	const state = agent ? (agent.error ? "broken" : jobState(lastJobFor(jobs, "agent.check", agent.path, agent.id))) : null;
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="agent-detail-screen">
			<PageHeader
				title={
					<span className="flex min-w-0 items-center gap-3">
						<AgentAvatar name={agentId} size="lg" />
						<span className="truncate">{agentId}</span>
					</span>
				}
				subtitle={
					agent ? (
						<span className="flex flex-wrap items-center gap-x-3 gap-y-1">
							<span className="inline-flex items-center gap-1.5">
								{Icon && <Icon className="size-3.5" aria-hidden="true" />}
								{adapterLabel(agent.adapter)}
							</span>
							{agent.model && (
								<span className="inline-flex items-center gap-1.5 font-mono text-xs" data-testid="agent-detail-model">
									<Cpu className="size-3.5" aria-hidden="true" />
									{agent.model}
								</span>
							)}
							{capabilities.length > 0 && (
								<span className="inline-flex flex-wrap items-center gap-1" data-testid="agent-detail-capabilities" aria-label={t("detail.capabilities")}>
									{capabilities.map((cap) => (
										<span key={cap} className="bg-muted/60 rounded-md border px-1.5 py-0.5 font-mono text-[11px]" data-testid={`agent-capability-${cap}`}>
											{cap}
										</span>
									))}
								</span>
							)}
						</span>
					) : (
						t("detail.subtitle", { id: agentId })
					)
				}
				meta={state && <CheckPill state={state} testId="agent-detail-status" />}
				breadcrumbs={[{ label: t("detail.back"), to: "/agents" }, { label: agentId }]}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						<Button asChild size="sm" variant="ghost">
							<Link to="/agents/$agentId" params={{ agentId }} search={{ tab: "config" } as never} data-testid="agent-detail-edit">
								<Pencil aria-hidden="true" />
								{tu("actions.edit")}
							</Link>
						</Button>
						<Button asChild size="sm" variant="outline">
							<Link to="/agents/$agentId" params={{ agentId }} search={{ tab: "checks" } as never} data-testid="agent-detail-check">
								<ListChecks aria-hidden="true" />
								{tu("actions.check")}
							</Link>
						</Button>
						<Button asChild size="sm" variant="ghost">
							<Link to={"/evaluate" as never} search={{ agent: agentId, mode: "versions" } as never} data-testid="agent-detail-compare-versions">
								<GitCompare aria-hidden="true" />
								{t("detail.compareVersions")}
							</Link>
						</Button>
						<Button asChild size="sm" variant="outline">
							<Link to="/experiments/new" search={{ agent: agentId } as never} data-testid="agent-detail-run">
								<Play aria-hidden="true" />
								{tu("actions.run")}
							</Link>
						</Button>
						<Button asChild size="sm">
							<Link to={"/evaluate" as never} search={{ agent: agentId } as never} data-testid="agent-detail-evaluate">
								<Gauge aria-hidden="true" />
								{t("detail.evaluate")}
							</Link>
						</Button>
					</div>
				}
			/>
			{agent?.adapter === "command" && agent.image && <AgentImageApproval image={agent.image} />}
			{agent && (agent.adapter === "remote_http" || agent.adapter === "openai_agent") && <SavedEndpointTest agentId={agentId} />}
			<RouteTabs tabs={tabs} defaultTab="config" label={t("detail.tabs.label")} testId="agent-detail-tabs">
				{(active) =>
					active === "checks" ? (
						<ChecksTab agentId={agentId} />
					) : active === "tools" ? (
						<ToolsTab agentId={agentId} />
					) : active === "versions" ? (
						<VersionsTab agentId={agentId} />
					) : active === "report-card" ? (
						<ReportCardTab agentId={agentId} />
					) : (
						<ConfigTab agentId={agentId} />
					)
				}
			</RouteTabs>
		</section>
	);
}
