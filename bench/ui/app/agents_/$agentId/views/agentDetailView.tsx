import { CopyAgentDialog } from "@/components/agents/copyAgentDialog";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { DeleteAgentDialog } from "@/components/agents/deleteAgentDialog";
import "@/components/agents/fw13I18n";
import type { OverflowMenuItem } from "@/components/agents/overflowMenu";
import { SkillScanBar } from "@/components/agents/skillScanBar";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { EmptyNote } from "@/components/runs/screenStates";
import "@/components/platform/w4b03I18n";
import "@/lib/types/agentFirstI18n";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useGetAgentConfigQuery } from "@/lib/store/apis/agentBuilderApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentCompatibilityQuery, useGetAgentVersionsQuery } from "@/lib/store/apis/evaluationsApi";
import { Activity, Bug, Copy, Cpu, FlaskConical, Gauge, GitCompare, ListChecks, PencilLine, ShieldAlert, Trash2 } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { notify } from "@/lib/notify";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { CheckPill, adapterIcon, agentRunsTotal, type CheckState, jobState, lastJobFor, useAdapterLabel } from "../../../agents/views/agentDisplay";
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

/** Which dialog of the More menu is open: delete, duplicate or rename (M2-41: menu actions, no URL param). */
export type AgentDialog = "delete" | "duplicate" | "rename" | null;

/**
 * The check pill state to show in the header, or null to hide it: "Setup not checked" hides on an
 * agent that already has runs (M2-22, the same rule as the agent list).
 */
export function headerCheckState(state: CheckState | null, runs: number): CheckState | null {
	if (state === "never" && runs > 0) return null;
	return state;
}

/**
 * One agent (/agents/$agentId?tab=): name, version, capabilities, one primary Evaluate button and a
 * "more" menu, then Report card (default), Versions, Runs, Setup, Tools and Pull requests (test on
 * every pull request). ?tab=checks (the readiness check) and ?tab=compare-models open from the menu and
 * then show as the last tab. The menu's "Delete agent", "Duplicate agent" and "Rename agent" open their
 * dialog from page state (no URL param, so a pasted link opens nothing); they are offered only to members,
 * and only for an agent whose settings file lives in this project (built-in library agents have none).
 */
export default function AgentDetailView() {
	const { t } = useTranslation("agentFirst");
	const { t: tb } = useTranslation("agentBuilder");
	const { t: tf } = useTranslation("fw13");
	const { t: tn } = useTranslation("w4b03");
	const { agentId } = useParams({ from: "/agents_/$agentId" });
	const navigate = useNavigate();
	const session = useSession();
	const writable = canUse(session, "member");
	// GET /agents/{id}/config reads the project's file: a built-in agent has none (404).
	const config = useGetAgentConfigQuery(agentId, { skip: !writable });
	const canDelete = writable && config.isSuccess;
	const [dialog, setDialog] = useState<AgentDialog>(null);
	const [, setTabParam] = useQueryState("tab", parseAsString.withOptions({ history: "replace" }));
	const copyMode = dialog === "rename" || dialog === "duplicate" ? dialog : null;
	const { agent, isLoading: agentLoading } = useAgentSummary(agentId);
	usePageTitle(agent ? `${agentId} · ${t("app.name", { ns: "bench" })}` : undefined);
	const jobs = useAgentJobs();
	const adapterLabel = useAdapterLabel();
	const compatibility = useGetAgentCompatibilityQuery(agentId);
	const versions = useGetAgentVersionsQuery(agentId);
	const capabilities = compatibility.data?.capabilities ?? [];
	const version = latestVersion(versions.data?.versions);
	const tabs: RouteTab[] = [
		{ id: "report-card", label: t("detail.tabs.reportCard") },
		{
			id: "versions",
			label: t("detail.tabs.versions"),
			badge: versions.data?.versions?.length || undefined,
		},
		{ id: "runs", label: t("detail.tabs.runs") },
		{ id: "setup", label: t("detail.tabs.setup") },
		{ id: "tools", label: t("detail.tabs.tools") },
		{ id: "pull-requests", label: t("detail.tabs.pullRequests") },
		{
			id: "checks",
			label: t("detail.tabs.checks"),
			hidden: true,
			addressable: true,
		},
		{
			id: "compare-models",
			label: t("detail.tabs.compareModels"),
			hidden: true,
			addressable: true,
		},
		{ id: "config", label: t("detail.tabs.setup"), hidden: true },
	];
	const Icon = agent ? adapterIcon(agent.adapter) : null;
	// The same rule as the live monitor list: an agent that owns its model sits behind a remote endpoint.
	const endpointAgent = agent?.owns_model === true;
	const state = agent
		? headerCheckState(agent.error ? "broken" : jobState(lastJobFor(jobs, "agent.check", agent.path, agent.id)), agentRunsTotal(agent))
		: null;
	const menu: OverflowMenuItem[] = [
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
			to: "/failures",
			search: { tab: "agents", agent: agentId },
			icon: Bug,
			testId: "agent-detail-why-fail",
		},
		// Only an agent with a remote endpoint can be live monitored; any other gets the drift form (M7-54).
		endpointAgent
			? {
					label: t("detail.menu.monitor"),
					to: "/scheduled-checks",
					search: { tab: "live", agent: agentId },
					icon: Activity,
					testId: "agent-detail-monitor",
				}
			: {
					label: t("detail.menu.monitorDrift"),
					to: "/scheduled-checks",
					search: { tab: "drift", agent: agentId },
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
						onSelect: () => setDialog("duplicate"),
						separatorBefore: true,
						icon: Copy,
						testId: "agent-detail-duplicate",
					},
					{
						label: tf("copyAgent.menuRename"),
						onSelect: () => setDialog("rename"),
						icon: PencilLine,
						testId: "agent-detail-rename",
					},
					{
						label: tf("deleteAgent.menu"),
						onSelect: () => setDialog("delete"),
						destructive: true,
						separatorBefore: true,
						icon: Trash2,
						testId: "agent-detail-delete",
					},
				]
			: []),
	];
	if (!agentLoading && !agent) {
		return (
			<section className={cn(pageWidthClass("dash"), "flex flex-col gap-4")} data-page-width="dash" data-testid="agent-detail-screen">
				<EmptyNote testId="agent-not-found">{tn("agentNotFound.text", { id: agentId })}</EmptyNote>
				<div>
					<Button asChild size="sm" variant="outline">
						<Link to="/agents" data-testid="agent-not-found-back">
							{tn("agentNotFound.back")}
						</Link>
					</Button>
				</div>
			</section>
		);
	}
	return (
		<section className={cn(pageWidthClass("dash"), "flex flex-col gap-6")} data-page-width="dash" data-testid="agent-detail-screen">
			<PageHeader
				// The heading is the agent's name only (M2-30): the avatar sits before it, hidden from
				// screen readers, and the version chip sits beside it with the check pill.
				title={<span className="block truncate">{agentId}</span>}
				subtitle={
					agent ? (
						<span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
							<span className="inline-flex min-w-0 items-center gap-1.5">
								{Icon && <Icon className="size-3.5 shrink-0" aria-hidden="true" />}
								<span className="truncate">{adapterLabel(agent.adapter)}</span>
							</span>
							{agent.model && (
								<span className="inline-flex items-center gap-1.5 font-mono text-xs" data-testid="agent-detail-model">
									<Cpu className="size-3.5" aria-hidden="true" />
									{agent.model}
								</span>
							)}
							{capabilities.length > 0 && (
								<span
									className="hidden flex-wrap items-center gap-1 sm:inline-flex"
									data-testid="agent-detail-capabilities"
									aria-label={tb("detail.capabilities")}
								>
									{capabilities.map((cap) => (
										<span key={cap} className="bg-muted/60 rounded-md border px-1.5 py-0.5 font-mono text-[11px]" data-testid={`agent-capability-${cap}`}>
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
				meta={
					<>
						<span
							className="bg-muted text-muted-foreground shrink-0 rounded-md px-1.5 py-0.5 font-mono text-xs font-medium"
							data-testid="agent-detail-version"
							title={version === null ? t("detail.noVersionHint") : undefined}
						>
							{version === null ? t("detail.noVersion") : `v${version}`}
						</span>
						{state && <CheckPill state={state} runs={agent ? agentRunsTotal(agent) : undefined} testId="agent-detail-status" />}
					</>
				}
				breadcrumbs={[{ label: tb("detail.back"), to: "/agents" }, { label: agentId }]}
				actions={
					<Button asChild size="sm">
						<Link to={"/evaluate" as never} search={{ agent: agentId } as never} data-testid="agent-detail-evaluate">
							<Gauge aria-hidden="true" />
							{tb("detail.evaluate")}
						</Link>
					</Button>
				}
				moreLabel={t("detail.more")}
				more={menu}
			/>
			{canDelete && dialog === "delete" && (
				<DeleteAgentDialog
					agentId={agentId}
					onClose={() => setDialog(null)}
					onDeleted={() => {
						notify({ text: t("deleted", { ns: "toast", name: agentId }) });
						void navigate({ to: "/agents" });
					}}
				/>
			)}
			{canDelete && copyMode !== null && (
				<CopyAgentDialog
					key={`${agentId}-${copyMode}`}
					agentId={agentId}
					mode={copyMode}
					onClose={() => setDialog(null)}
					onCopied={(newId) => {
						notify({ text: t("saved", { ns: "toast", name: newId }) });
						void navigate({ to: "/agents/$agentId", params: { agentId: newId } });
					}}
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
						<ConfigTab agentId={agentId} checkFailed={state === "failed" || state === "broken"} />
					) : (
						<ReportCardTab agentId={agentId} />
					)
				}
			</RouteTabs>
		</section>
	);
}
