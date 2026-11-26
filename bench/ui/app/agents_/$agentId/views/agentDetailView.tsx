import { CopyAgentDialog } from "@/components/agents/copyAgentDialog";
import { DeleteAgentDialog } from "@/components/agents/deleteAgentDialog";
import "@/components/agents/fw13I18n";
import { OverflowMenu } from "@/components/agents/overflowMenu";
import { SkillScanBar } from "@/components/agents/skillScanBar";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button } from "@/components/ui/button";
import "@/lib/types/agentFirstI18n";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useGetAgentConfigQuery } from "@/lib/store/apis/agentBuilderApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentCompatibilityQuery, useGetAgentVersionsQuery } from "@/lib/store/apis/evaluationsApi";
import { Activity, Bug, Copy, Cpu, FlaskConical, Gauge, GitCompare, ListChecks, PencilLine, ShieldAlert, Trash2 } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { AgentAvatar, CheckPill, adapterIcon, jobState, lastJobFor, useAdapterLabel } from "../../../agents/views/agentDisplay";
import { useAgentJobs } from "../../../agents/views/agentJobs";
import { AgentImageApproval } from "@/components/sharedpool/imageRequests";
import { SavedEndpointTest } from "../../new/views/connectForm";
import { useAgentSummary } from "./agentOps.shared";
import AgentRunsTab from "./agentRunsTab";
import ChecksTab from "./checksTab";
import { CompareModelsForm } from "@/components/modelswitch/compareModelsForm";
import ConfigTab from "./configTab";
import PullRequestsTab from "./pullRequestsTab";
import ReportCardTab from "./reportCardTab";
import ToolsTab from "./toolsTab";
import VersionsTab from "./versionsTab";

/** The newest saved version number, or null when the agent has none. */
export function latestVersion(versions: ReadonlyArray<{ version: number }> | undefined): number | null {
	const list = versions ?? [];
	return list.length === 0 ? null : Math.max(...list.map((v) => v.version));
}

/**
 * One agent (/agents/$agentId?tab=): name, version, capabilities, one primary Evaluate button and a
 * "more" menu, then Report card (default), Versions, Runs, Setup, Tools and Pull requests (test on
 * every pull request). ?tab=checks (the readiness check) and ?tab=compare-models open from the menu and
 * then show as the last tab. ?delete=1 (the menu's "Delete agent") asks to delete the agent; it is
 * offered only to members, and only for an agent whose settings file lives in this project (built-in
 * library agents have none). ?copy=duplicate and ?copy=rename (the menu's "Duplicate agent" and "Rename agent")
 * ask for a new name under the same rule.
 */
export default function AgentDetailView() {
	const { t } = useTranslation("agentFirst");
	const { t: tb } = useTranslation("agentBuilder");
	const { t: tf } = useTranslation("fw13");
	const { agentId } = useParams({ from: "/agents_/$agentId" });
	const navigate = useNavigate();
	const session = useSession();
	const writable = canUse(session, "member");
	// GET /agents/{id}/config reads the project's file: a built-in agent has none (404).
	const config = useGetAgentConfigQuery(agentId, { skip: !writable });
	const canDelete = writable && config.isSuccess;
	const [deleteParam, setDeleteParam] = useQueryState("delete", parseAsString.withOptions({ history: "replace" }));
	const [copyParam, setCopyParam] = useQueryState("copy", parseAsString.withOptions({ history: "replace" }));
	const [, setTabParam] = useQueryState("tab", parseAsString.withOptions({ history: "replace" }));
	const copyMode = copyParam === "rename" ? "rename" : copyParam === "duplicate" ? "duplicate" : null;
	const { agent } = useAgentSummary(agentId);
	usePageTitle(agent ? `${agentId} · ${t("app.name", { ns: "bench" })}` : undefined);
	const jobs = useAgentJobs();
	const adapterLabel = useAdapterLabel();
	const compatibility = useGetAgentCompatibilityQuery(agentId);
	const versions = useGetAgentVersionsQuery(agentId);
	const capabilities = compatibility.data?.capabilities ?? [];
	const version = latestVersion(versions.data?.versions);
	const tabs: RouteTab[] = [
		{ id: "report-card", label: t("detail.tabs.reportCard") },
		{ id: "versions", label: t("detail.tabs.versions"), badge: versions.data?.versions?.length || undefined },
		{ id: "runs", label: t("detail.tabs.runs") },
		{ id: "setup", label: t("detail.tabs.setup") },
		{ id: "tools", label: t("detail.tabs.tools") },
		{ id: "pull-requests", label: t("detail.tabs.pullRequests") },
		{ id: "checks", label: t("detail.tabs.checks"), hidden: true, addressable: true },
		{ id: "compare-models", label: t("detail.tabs.compareModels"), hidden: true, addressable: true },
		{ id: "config", label: t("detail.tabs.setup"), hidden: true },
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
						<span
							className="bg-muted text-muted-foreground shrink-0 rounded-md px-1.5 py-0.5 font-mono text-xs font-medium"
							data-testid="agent-detail-version"
							title={version === null ? t("detail.noVersionHint") : undefined}
						>
							{version === null ? t("detail.noVersion") : `v${version}`}
						</span>
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
								<span
									className="inline-flex flex-wrap items-center gap-1"
									data-testid="agent-detail-capabilities"
									aria-label={tb("detail.capabilities")}
								>
									{capabilities.map((cap) => (
										<span
											key={cap}
											className="bg-muted/60 rounded-md border px-1.5 py-0.5 font-mono text-[11px]"
											data-testid={`agent-capability-${cap}`}
										>
											{cap}
										</span>
									))}
								</span>
							)}
						</span>
					) : (
						tb("detail.subtitle", { id: agentId })
					)
				}
				meta={state && <CheckPill state={state} testId="agent-detail-status" />}
				breadcrumbs={[{ label: tb("detail.back"), to: "/agents" }, { label: agentId }]}
				actions={
					<div className="flex items-center gap-2">
						<Button asChild size="sm">
							<Link to={"/evaluate" as never} search={{ agent: agentId } as never} data-testid="agent-detail-evaluate">
								<Gauge aria-hidden="true" />
								{tb("detail.evaluate")}
							</Link>
						</Button>
						<OverflowMenu
							label={t("detail.more")}
							testId="agent-detail-more"
							items={[
								{
									label: t("detail.menu.compareVersions"),
									to: "/evaluate",
									search: { agent: agentId, mode: "versions" },
									icon: GitCompare,
									testId: "agent-detail-compare-versions",
								},
								{
									label: t("detail.menu.compareModels"),
									to: "/agents/$agentId",
									params: { agentId },
									search: { tab: "compare-models" },
									icon: Cpu,
									testId: "agent-detail-compare-models",
								},
								{
									label: t("detail.menu.failureTest"),
									to: "/failure-tests",
									search: { agent: agentId },
									icon: ShieldAlert,
									testId: "agent-detail-failure-test",
								},
								{
									label: t("detail.menu.whyFail"),
									to: "/why-agents-fail",
									search: { agent: agentId },
									icon: Bug,
									testId: "agent-detail-why-fail",
								},
								{
									label: t("detail.menu.monitor"),
									to: "/scheduled-checks",
									search: { tab: "live", agent: agentId },
									icon: Activity,
									testId: "agent-detail-monitor",
								},
								{
									label: t("detail.menu.check"),
									to: "/agents/$agentId",
									params: { agentId },
									search: { tab: "checks" },
									icon: ListChecks,
									testId: "agent-detail-check",
								},
								{
									label: t("detail.menu.custom"),
									to: "/experiments/new",
									search: { agent: agentId },
									icon: FlaskConical,
									testId: "agent-detail-run",
								},
								...(canDelete
									? [
											{
												label: tf("copyAgent.menuDuplicate"),
												to: "/agents/$agentId",
												params: { agentId },
												search: { copy: "duplicate" },
												icon: Copy,
												testId: "agent-detail-duplicate",
											},
											{
												label: tf("copyAgent.menuRename"),
												to: "/agents/$agentId",
												params: { agentId },
												search: { copy: "rename" },
												icon: PencilLine,
												testId: "agent-detail-rename",
											},
											{
												label: tf("deleteAgent.menu"),
												to: "/agents/$agentId",
												params: { agentId },
												search: { delete: "1" },
												icon: Trash2,
												testId: "agent-detail-delete",
											},
										]
									: []),
							]}
						/>
					</div>
				}
			/>
			{canDelete && deleteParam !== null && (
				<DeleteAgentDialog agentId={agentId} onClose={() => void setDeleteParam(null)} onDeleted={() => void navigate({ to: "/agents" })} />
			)}
			{canDelete && copyMode !== null && (
				<CopyAgentDialog
					key={`${agentId}-${copyMode}`}
					agentId={agentId}
					mode={copyMode}
					onClose={() => void setCopyParam(null)}
					onCopied={(newId) => void navigate({ to: "/agents/$agentId", params: { agentId: newId } })}
				/>
			)}
			{agent?.adapter === "command" && agent.image && <AgentImageApproval image={agent.image} />}
			{/* Fix wave 3, L5: runs are refused while an uploaded skill has not passed a scan. */}
			{writable && <SkillScanBar agentId={agentId} onOpenTools={() => void setTabParam("tools")} />}
			{agent && (agent.adapter === "remote_http" || agent.adapter === "openai_agent") && <SavedEndpointTest agentId={agentId} />}
			<RouteTabs tabs={tabs} defaultTab="report-card" label={tb("detail.tabs.label")} testId="agent-detail-tabs">
				{(active) =>
					active === "checks" ? (
						<ChecksTab agentId={agentId} />
					) : active === "compare-models" ? (
						<CompareModelsForm agentId={agentId} />
					) : active === "tools" ? (
						<ToolsTab agentId={agentId} />
					) : active === "versions" ? (
						<VersionsTab agentId={agentId} />
					) : active === "pull-requests" ? (
						<PullRequestsTab agentId={agentId} />
					) : active === "runs" ? (
						<AgentRunsTab agentId={agentId} />
					) : active === "setup" || active === "config" ? (
						<ConfigTab agentId={agentId} />
					) : (
						<ReportCardTab agentId={agentId} />
					)
				}
			</RouteTabs>
		</section>
	);
}