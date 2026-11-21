import { ClusterDetailPanel, ClusterRunLink } from "@/components/clusters/clusterDetail";
import { ClustersBanner } from "@/components/clusters/clustersBanner";
import { ClustersFilters, type ClusterTextFilter } from "@/components/clusters/clustersFilters";
import { ClustersSummary } from "@/components/clusters/clustersSummary";
import { ClustersTable } from "@/components/clusters/clustersTable";
import {
	clusterParams,
	excludesSynthetic,
	formatExpList,
	hasClusterFilters,
	isNotFound,
	parseExpList,
	type ClusterQuery,
} from "@/components/clusters/clusters.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { runLocations, type RunLocation } from "@/components/triage/triage.utils";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useGetClusterQuery, useGetClustersQuery, useGetGridQuery } from "@/lib/store/apis/benchApi";
import {
	CLUSTER_SYNTHETIC,
	type ClusterListResponse,
	type ClusterParams,
	type ClusterRun,
	type ClusterSynthetic,
	type GridResponse,
} from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { parseAsInteger, parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * The Clusters tab of /inbox: ?tab=clusters&suite=&agent=&exp=&since=&min=&synthetic=&sig= (the
 * old /clusters redirects here with its filters). Every filter goes to
 * the server; an absent one takes the server default. sig opens one cluster below the table.
 * Clusters are observed groupings of failed runs, never root causes (docs/failure-clusters.md).
 */
export default function ClustersTab() {
	const { t } = useTranslation("bench");
	const [suite, setSuite] = useQueryState("suite", parseAsString);
	const [agent, setAgent] = useQueryState("agent", parseAsString);
	const [exp, setExp] = useQueryState("exp", parseAsString);
	const [since, setSince] = useQueryState("since", parseAsString);
	const [min, setMin] = useQueryState("min", parseAsInteger);
	const [synthetic, setSynthetic] = useQueryState("synthetic", parseAsStringLiteral(CLUSTER_SYNTHETIC));
	const [sig, setSig] = useQueryState("sig", parseAsString);

	const query = useMemo<ClusterQuery>(() => ({ suite, agent, exp, since, min, synthetic }), [suite, agent, exp, since, min, synthetic]);
	const params = useMemo(() => clusterParams(query), [query]);
	const list = useGetClustersQuery(params);
	const data = list.data;

	const onText = (filter: ClusterTextFilter, value: string) => {
		const next = value || null;
		if (filter === "suite") void setSuite(next);
		else if (filter === "agent") void setAgent(next);
		else if (filter === "since") void setSince(next);
		else void setExp(formatExpList(parseExpList(value)));
	};
	const resetFilters = () => {
		void setSuite(null);
		void setAgent(null);
		void setExp(null);
		void setSince(null);
		void setMin(null);
		void setSynthetic(null);
	};
	const agentOptions = useMemo(() => [...new Set((data?.clusters ?? []).flatMap((c) => c.agents ?? []))].sort(), [data?.clusters]);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="clusters-screen">
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground max-w-3xl text-sm">{t("clusters.subtitle")}</p>
			</div>
			<ClustersBanner />

			<ClustersFilters
				suite={suite ?? data?.filters?.suite ?? ""}
				agent={agent ?? data?.filters?.agent ?? ""}
				exp={exp ?? formatExpList(data?.filters?.experiments ?? []) ?? ""}
				since={since ?? data?.filters?.since ?? ""}
				min={min ?? data?.filters?.min ?? 1}
				synthetic={synthetic ?? syntheticOf(data?.filters?.synthetic)}
				agentOptions={agentOptions}
				experimentOptions={data?.experiments ?? []}
				onText={onText}
				onMin={(next) => void setMin(next)}
				onSynthetic={(next) => void setSynthetic(next)}
				disabled={list.isLoading}
			/>

			{list.isLoading ? (
				<BlockSkeleton rows={6} testId="clusters-loading" />
			) : list.isError || !data ? (
				<div className="flex flex-col gap-2">
					<LoadError title={t("clusters.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="clusters-error" />
					{hasClusterFilters(query) && (
						<Button variant="outline" size="sm" className="self-start" onClick={resetFilters} dataTestId="clusters-reset-filters">
							{t("clusters.resetFilters")}
						</Button>
					)}
				</div>
			) : (
				<div className={cn("flex min-w-0 flex-col gap-6 transition-opacity", list.isFetching && "opacity-60")} aria-busy={list.isFetching}>
					<ClustersSummary data={data} />
					<Section title={t("clusters.table.title")} description={t("clusters.table.description")} testId="clusters-table-section">
						{(data.clusters ?? []).length === 0 ? (
							<EmptyClusters data={data} onShowSynthetic={() => void setSynthetic("include")} />
						) : (
							<div className="min-w-0 rounded-sm border">
								<ClustersTable clusters={data.clusters} selected={sig} onSelect={(next) => void setSig(next)} />
							</div>
						)}
					</Section>
					{sig && <ClusterPanel signature={sig} params={params} onClose={() => void setSig(null)} onOpen={(next) => void setSig(next)} />}
				</div>
			)}
		</section>
	);
}

/** The server's synthetic filter as a switch value; anything else reads as the default, exclude. */
function syntheticOf(value: string | undefined): ClusterSynthetic {
	return (CLUSTER_SYNTHETIC as readonly string[]).includes(value ?? "") ? (value as ClusterSynthetic) : "exclude";
}

/** Says why the table is empty; when synthetic runs were left out, offers them. */
export function EmptyClusters({ data, onShowSynthetic }: { data: ClusterListResponse; onShowSynthetic: () => void }) {
	const { t } = useTranslation("bench");
	const message =
		data.runs === 0
			? t("clusters.empty.noRuns")
			: data.failures === 0
				? t("clusters.empty.noFailures", { count: data.runs })
				: t("clusters.empty.allHidden");
	if (data.filters?.synthetic !== "include" && data.filters?.synthetic !== "only" && excludesSynthetic(data.excluded)) {
		return (
			<div className="flex flex-col items-center gap-3 rounded-sm border border-dashed px-4 py-6" data-testid="clusters-empty">
				<p className="text-muted-foreground text-center text-sm">{message}</p>
				<Button variant="outline" size="sm" onClick={onShowSynthetic} dataTestId="clusters-show-synthetic">
					{t("clusters.empty.showSynthetic")}
				</Button>
			</div>
		);
	}
	return <EmptyNote testId="clusters-empty">{message}</EmptyNote>;
}

/** Loads one cluster with the list's filters. A 404 means it is not in the filtered set, not that it failed. */
function ClusterPanel({
	signature,
	params,
	onClose,
	onOpen,
}: {
	signature: string;
	params: ClusterParams;
	onClose: () => void;
	onOpen: (signature: string) => void;
}) {
	const { t } = useTranslation("bench");
	const detail = useGetClusterQuery({ signature, ...params });
	return (
		<Card className="px-4 py-4 shadow-none" data-testid="cluster-panel">
			{detail.isLoading ? (
				<BlockSkeleton rows={5} testId="cluster-detail-loading" />
			) : isNotFound(detail.error) ? (
				<div className="flex flex-col gap-2">
					<EmptyNote testId="cluster-detail-not-found">{t("clusters.detail.notFound")}</EmptyNote>
					<Button variant="outline" size="sm" className="self-start" onClick={onClose} dataTestId="cluster-detail-not-found-close">
						{t("clusters.detail.close")}
					</Button>
				</div>
			) : detail.error || !detail.data ? (
				<LoadError
					title={t("clusters.detail.loadError")}
					error={detail.error}
					onRetry={() => void detail.refetch()}
					testId="cluster-detail-error"
				/>
			) : (
				<ClusterDetailPanel detail={detail.data} onClose={onClose} onOpen={onOpen} renderRunLink={(run) => <RunPairLink run={run} />} />
			)}
		</Card>
	);
}

// Run locations per grid response, so every row of one experiment shares one lookup table.
const locationCache = new WeakMap<GridResponse, Map<string, RunLocation>>();

function locationsOf(grid: GridResponse): Map<string, RunLocation> {
	let found = locationCache.get(grid);
	if (!found) {
		found = runLocations(grid);
		locationCache.set(grid, found);
	}
	return found;
}

/**
 * A run id linked to its matched pair. The server sends pair_id; for a run without one (an older
 * server), the pair is looked up in the experiment's grid, one request per experiment shared by its rows.
 */
export function RunPairLink({ run }: { run: ClusterRun }) {
	const grid = useGetGridQuery(run.pair_id ? skipToken : run.exp_id);
	if (run.pair_id) return <ClusterRunLink runId={run.run_id} pairId={run.pair_id} />;
	const pairId = grid.data ? (locationsOf(grid.data).get(run.run_id)?.pairId ?? null) : grid.isLoading ? undefined : null;
	return <ClusterRunLink runId={run.run_id} pairId={pairId} />;
}