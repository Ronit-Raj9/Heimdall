import {
	SYNTHETIC_ON,
	SYNTHETIC_VALUES,
	commonSuites,
	diffParams,
	findVersion,
	suggestBaseline,
	versionLabel,
} from "@/components/agentdiff/agentDiff.utils";
import { ConfigDiffPanel } from "@/components/agentdiff/configDiffPanel";
import { DiffSummary, VersionCards } from "@/components/agentdiff/diffSummary";
import { GateChanges, MetricDeltasTable } from "@/components/agentdiff/metricDeltas";
import { TaskFlipsTable } from "@/components/agentdiff/taskFlipsTable";
import { VersionPicker } from "@/components/agentdiff/versionPicker";
import "@/components/agentdiff/gw03I18n";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { useGetAgentDiffQuery, useGetAgentDiffVersionsQuery } from "@/lib/store/apis/agentDiffApi";
import type { AgentDiffResponse } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { AlertTriangle, Info } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

const searchParsers = {
	a: parseAsString,
	b: parseAsString,
	suite: parseAsString,
	condition: parseAsString,
	agent: parseAsString,
	synthetic: parseAsStringLiteral(SYNTHETIC_VALUES),
	system: parseAsString,
};

/**
 * The Diff tab of /agents: ?tab=diff&a=&b=&suite=&condition=&agent=&synthetic=1 (the old
 * /agents/diff redirects here with its query). What changed between
 * version A and version B of an agent (config half) and which tasks flipped (outcome half), over
 * verified sealed runs. With only b (a link from the leaderboard), A is that agent's previous
 * version; with only agent (a link from the agents page), the server compares its two newest.
 */
export default function AgentDiffTab() {
	const { t } = useTranslation("bench");
	const { t: tg } = useTranslation("gw03");
	const [search, setSearch] = useQueryStates(searchParsers);
	const versionsQuery = useGetAgentDiffVersionsQuery();
	const versions = useMemo(() => versionsQuery.data?.versions ?? [], [versionsQuery.data]);

	const suggested = !search.a && search.b ? suggestBaseline(versions, search.b) : null;
	const params = useMemo(() => diffParams(search, suggested?.config_hash ?? null), [search, suggested]);
	const diff = useGetAgentDiffQuery(params ?? skipToken);
	const report = params ? diff.data : undefined;

	const agentMode = !search.a && !search.b && Boolean(search.agent);
	const shownA = search.a ?? suggested?.config_hash ?? (agentMode ? report?.a.config_hash : undefined) ?? "";
	const shownB = search.b ?? (agentMode ? report?.b.config_hash : undefined) ?? "";
	const suites = commonSuites(findVersion(versions, shownA), findVersion(versions, shownB));
	const mode = report?.mode ?? versionsQuery.data?.mode;
	const synthetic = mode === "synthetic" || Boolean(report?.synthetic);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="agentdiff-screen">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("agentDiff.subtitle")}</p>
			{synthetic && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="agentdiff-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agentDiff.synthetic")}
				</p>
			)}

			<VersionPicker
				versions={versions}
				a={shownA}
				b={shownB}
				suite={search.suite ?? ""}
				condition={search.condition ?? ""}
				synthetic={search.synthetic === SYNTHETIC_ON}
				suites={suites}
				onA={(next) => void setSearch({ a: next, b: search.b ?? (shownB || null), agent: null })}
				onB={(next) => void setSearch({ b: next, a: search.a ?? (agentMode && shownA ? shownA : null), agent: null })}
				onSwap={() => void setSearch({ a: shownB || null, b: shownA || null, agent: null })}
				onScope={({ suite, condition }) => void setSearch({ suite: suite || null, condition: condition || null })}
				onSynthetic={(on) => void setSearch({ synthetic: on ? SYNTHETIC_ON : null })}
				showSystem={search.system === "1"}
				onShowSystem={(on) => void setSearch({ system: on ? "1" : null })}
				disabled={versionsQuery.isLoading}
			/>
			{suggested && (
				<p className="text-muted-foreground flex items-start gap-2 text-xs" role="note" data-testid="agentdiff-suggested-note">
					<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					{t("agentDiff.picker.suggested", { agent: suggested.agent_id, version: versionLabel(suggested, tg("diffPicker.unsaved")) })}
				</p>
			)}
			{agentMode && (
				<p className="text-muted-foreground flex items-start gap-2 text-xs" role="note" data-testid="agentdiff-agent-note">
					<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					{t("agentDiff.picker.agentMode", { agent: search.agent })}
				</p>
			)}
			{versionsQuery.isError && (
				<LoadError
					title={t("agentDiff.versionsError")}
					error={versionsQuery.error}
					onRetry={() => void versionsQuery.refetch()}
					testId="agentdiff-versions-error"
				/>
			)}

			{!params ? (
				<NothingAsked
					loading={versionsQuery.isLoading}
					noVersions={versionsQuery.isSuccess && versions.length === 0}
					onlyB={search.b && !search.a ? (findVersion(versions, search.b)?.agent_id ?? search.b) : null}
				/>
			) : diff.isLoading ? (
				<BlockSkeleton rows={8} testId="agentdiff-loading" />
			) : diff.isError || !report ? (
				<LoadError title={t("agentDiff.loadError")} error={diff.error} onRetry={() => void diff.refetch()} testId="agentdiff-error" />
			) : (
				<Report report={report} isFetching={diff.isFetching} />
			)}
		</section>
	);
}

/** Says what to do before there is anything to compare. */
function NothingAsked({ loading, noVersions, onlyB }: { loading: boolean; noVersions: boolean; onlyB: string | null }) {
	const { t } = useTranslation("bench");
	if (loading) return <BlockSkeleton rows={4} testId="agentdiff-versions-loading" />;
	if (noVersions) return <EmptyNote testId="agentdiff-no-versions">{t("agentDiff.noVersions")}</EmptyNote>;
	if (onlyB) return <EmptyNote testId="agentdiff-pick-a">{t("agentDiff.pickA", { agent: onlyB })}</EmptyNote>;
	return <EmptyNote testId="agentdiff-pick">{t("agentDiff.pick")}</EmptyNote>;
}

function Report({ report, isFetching }: { report: AgentDiffResponse; isFetching: boolean }) {
	const { t } = useTranslation("bench");
	const { outcome } = report;
	return (
		<div
			className={cn("flex min-w-0 flex-col gap-6 transition-opacity", isFetching && "opacity-60")}
			aria-busy={isFetching}
			data-testid="agentdiff-report"
		>
			<DiffSummary report={report} />
			<VersionCards report={report} />
			<Section
				title={t("agentDiff.config.title")}
				description={t("agentDiff.config.description", { count: report.config.changes })}
				testId="agentdiff-config-section"
			>
				<ConfigDiffPanel config={report.config} />
			</Section>
			<Section title={t("agentDiff.tasks.title")} description={t("agentDiff.tasks.description")} testId="agentdiff-tasks-section">
				<TaskFlipsTable outcome={outcome} />
			</Section>
			<Section title={t("agentDiff.metrics.title")} description={t("agentDiff.metrics.description")} testId="agentdiff-metrics-section">
				<MetricDeltasTable metrics={outcome.metrics ?? []} />
			</Section>
			<Section title={t("agentDiff.gates.title")} description={t("agentDiff.gates.description")} testId="agentdiff-gates-section">
				<GateChanges gates={outcome.gates ?? []} />
			</Section>
		</div>
	);
}