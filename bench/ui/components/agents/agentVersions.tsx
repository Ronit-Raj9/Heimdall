import { newestFirst } from "@/components/agentdiff/agentDiff.utils";
import { formatWhen } from "@/components/platform/platform.utils";
import { BlockSkeleton, Section } from "@/components/runs/screenStates";
import { EntityLink } from "@/components/shell";
import { getLocale } from "@/lib/i18n";
import { useGetAgentDiffVersionsQuery } from "@/lib/store/apis/agentDiffApi";
import type { AgentDiffVersionInfo } from "@/lib/types/bench";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Distinct experiment ids of a version's arms, newest first. */
export function versionExperiments(version: Pick<AgentDiffVersionInfo, "arms">): string[] {
	return [...new Set(version.arms.map((arm) => arm.exp_id))];
}

/** The versions of one agent config that ran: hash, label, suites, experiments and sealed runs. */
export function AgentVersions({ agentId, actions }: { agentId: string; actions?: ReactNode }) {
	const { t } = useTranslation("bench");
	const query = useGetAgentDiffVersionsQuery();
	const versions = newestFirst((query.data?.versions ?? []).filter((version) => version.agent_id === agentId));
	return (
		<Section
			title={t("agents.versions.title")}
			description={t("agents.versions.description")}
			testId="agents-versions"
			actions={versions.length > 1 ? actions : undefined}
		>
			{query.isLoading ? (
				<BlockSkeleton rows={3} testId="agents-versions-loading" />
			) : query.isError ? (
				<p className="text-muted-foreground text-xs" data-testid="agents-versions-error">
					{t("agents.versions.error")}
				</p>
			) : versions.length === 0 ? (
				<p className="text-muted-foreground text-xs" data-testid="agents-versions-empty">
					{t("agents.versions.empty")}
				</p>
			) : (
				<ol className="flex min-w-0 flex-col divide-y rounded-sm border" data-testid="agents-versions-list">
					{versions.map((version) => (
						<VersionRow key={version.config_hash} version={version} />
					))}
				</ol>
			)}
		</Section>
	);
}

function VersionRow({ version }: { version: AgentDiffVersionInfo }) {
	const { t } = useTranslation("bench");
	const experiments = versionExperiments(version);
	const shown = experiments.slice(0, 3);
	return (
		<li className="flex min-w-0 flex-col gap-1 px-3 py-2 text-xs" data-testid={`agents-version-${version.config_hash.slice(-12)}`}>
			<div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
				<span className="text-foreground font-medium">{version.label || version.agent_id}</span>
				<code className="text-muted-foreground" title={version.config_hash}>
					{version.config_hash.replace(/^sha256:/, "").slice(0, 12)}
				</code>
				<span className="text-muted-foreground ml-auto tabular-nums">{t("agents.versions.runs", { count: version.sealed_runs })}</span>
			</div>
			<div className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
				{version.suites.map((suite) => (
					<EntityLink key={suite} kind="suite" id={suite}>
						<span className="font-mono">{suite}</span>
					</EntityLink>
				))}
				{shown.map((expId) => (
					<EntityLink key={expId} kind="experiment" id={expId} />
				))}
				{experiments.length > shown.length && <span>{t("agents.versions.more", { count: experiments.length - shown.length })}</span>}
				{version.latest_at && <span className="ml-auto">{formatWhen(version.latest_at, getLocale())}</span>}
			</div>
		</li>
	);
}