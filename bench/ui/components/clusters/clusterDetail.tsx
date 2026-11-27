import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { EmptyNote } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ClusterConfigChange, ClusterDetail, ClusterLayer, ClusterRelated, ClusterRun } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { GitCommitHorizontal, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatShare, isClusterLayerKind, isHiddenTestLayer } from "./clusters.utils";
import { ClusterScopeBadge } from "./clustersTable";

export interface ClusterDetailPanelProps {
	detail: Pick<ClusterDetail, "cluster" | "runs">;
	onClose: () => void;
	/** Opens another cluster (a related one) in this panel. */
	onOpen: (signature: string) => void;
	/** One run's id as a link to its matched pair. The screen looks the pair up in the grid; tests pass a stub. */
	renderRunLink: (run: ClusterRun) => React.ReactNode;
}

/**
 * One cluster: its signature layers, a config change it followed, related clusters, the normalized
 * error text as inert text, and every run with a link to the run page.
 */
export function ClusterDetailPanel({ detail, onClose, onOpen, renderRunLink }: ClusterDetailPanelProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { cluster } = detail;
	const runs = detail.runs ?? [];
	const related = cluster.related ?? [];
	const first = formatDateTime(cluster.first_seen, locale) ?? "-";
	const last = formatDateTime(cluster.last_seen, locale) ?? "-";

	return (
		<section id="cluster-detail" className="flex flex-col gap-5" data-testid={`cluster-detail-${cluster.signature}`}>
			<div className="flex flex-wrap items-start justify-between gap-2">
				<div className="flex min-w-0 flex-col gap-1">
					<h2 className="text-foreground text-sm font-semibold tracking-tight break-words" data-testid="cluster-detail-title">
						{cluster.title}
					</h2>
					<div className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs">
						<OutcomeBadge outcome={cluster.outcome} testId="cluster-detail-outcome" />
						<ClusterScopeBadge scope={cluster.scope} testId="cluster-detail-scope" />
						<span className="tabular-nums">
							{t("clusters.detail.counts", { count: cluster.count, share: formatShare(cluster.share, locale) })}
						</span>
						<span>{t("clusters.detail.seen", { first, last })}</span>
					</div>
					<p className="text-muted-foreground text-xs">
						{t("clusters.detail.signature")} <code className="font-mono text-xs">{cluster.signature}</code>
					</p>
				</div>
				<Button variant="ghost" size="sm" onClick={onClose} dataTestId="cluster-detail-close" aria-label={t("clusters.detail.close")}>
					<X className="size-4" aria-hidden="true" />
					{t("clusters.detail.close")}
				</Button>
			</div>

			{cluster.after_config_change && <ConfigChangeNotice change={cluster.after_config_change} />}

			<div className="grid gap-5 lg:grid-cols-2">
				<ClusterLayers layers={cluster.layers ?? []} />
				<div className="flex min-w-0 flex-col gap-5">
					{cluster.error_text && <ErrorText text={cluster.error_text} testId="cluster-error-text" />}
					{related.length > 0 && <RelatedClusters related={related} onOpen={onOpen} />}
				</div>
			</div>

			<div className="flex flex-col gap-2">
				<div className="flex flex-col gap-0.5">
					<h3 className="text-foreground text-sm font-medium">{t("clusters.detail.runs")}</h3>
					<p className="text-muted-foreground text-xs">{t("clusters.detail.runsHint")}</p>
				</div>
				{runs.length === 0 ? (
					<EmptyNote testId="cluster-runs-empty">{t("clusters.detail.noRuns")}</EmptyNote>
				) : (
					<ClusterRunsTable runs={runs} renderRunLink={renderRunLink} />
				)}
			</div>
		</section>
	);
}

/** The layers a signature was built from, most specific first. Failed hidden tests are listed as the run page lists them. */
export function ClusterLayers({ layers }: { layers: readonly ClusterLayer[] }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="cluster-layers">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-sm font-medium">{t("clusters.detail.layers")}</h3>
				<p className="text-muted-foreground text-xs">{t("clusters.detail.layersHint")}</p>
			</div>
			<ol className="flex flex-col divide-y rounded-sm border">
				{layers.map((layer, i) => {
					const kindLabel = isClusterLayerKind(layer.kind) ? t(`clusters.detail.layer.${layer.kind}`) : layer.kind;
					const items = layer.items ?? [];
					return (
						<li key={`${layer.kind}-${i}`} className="flex flex-col gap-1 px-3 py-2 text-xs" data-testid={`cluster-layer-${layer.kind}`}>
							{isHiddenTestLayer(layer) ? (
								<TestList title={kindLabel} tests={items} />
							) : (
								<>
									<span className="text-muted-foreground font-medium">{kindLabel}</span>
									{items.length > 0 ? (
										<ul className="flex flex-wrap gap-1">
											{items.map((item) => (
												<li key={item}>
													<code className="bg-muted text-foreground rounded-sm px-1 py-0.5 font-mono text-xs break-all">{item}</code>
												</li>
											))}
										</ul>
									) : (
										<span className="text-foreground font-mono text-xs break-all whitespace-pre-wrap">{layer.label}</span>
									)}
								</>
							)}
							{layer.source && (
								<span className="text-muted-foreground text-xs">{t("clusters.detail.source", { source: layer.source })}</span>
							)}
						</li>
					);
				})}
			</ol>
		</div>
	);
}

/**
 * Failed hidden test ids, with the same markup as the run page's list of grade.json's failed tests
 * (components/runs/claimVsResult.tsx TestList). Hidden test names are shown only here and there.
 */
function TestList({ title, tests }: { title: string; tests: readonly string[] }) {
	if (tests.length === 0) return null;
	return (
		<div className="flex flex-col gap-0.5" data-testid="cluster-hidden-tests">
			<span className="text-muted-foreground font-medium">{title}</span>
			<ul className="flex flex-col gap-0.5">
				{tests.map((test) => (
					<li key={test} className="text-chart-error-ink font-mono text-[11px] break-all">
						{test}
					</li>
				))}
			</ul>
		</div>
	);
}

/**
 * Recorded error text as an inert text node in a pre block: never markup, never a link, never
 * markdown. React escapes the string; nothing here parses it.
 */
export function ErrorText({ text, testId }: { text: string; testId: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-sm font-medium">{t("clusters.detail.errorText")}</h3>
				<p className="text-muted-foreground text-xs">{t("clusters.detail.errorTextHint")}</p>
			</div>
			<pre
				className="bg-muted/40 max-h-64 overflow-auto rounded-sm border px-3 py-2 text-xs leading-relaxed break-all whitespace-pre-wrap"
				data-testid={testId}
			>
				<code className="font-mono">{text}</code>
			</pre>
		</div>
	);
}

/** Every run of the cluster came after this agent's config hash changed. An observation, not a cause. */
function ConfigChangeNotice({ change }: { change: ClusterConfigChange }) {
	const { t } = useTranslation("bench");
	const at = formatDateTime(change.at, getLocale()) ?? change.at;
	return (
		<div
			role="note"
			data-testid="cluster-after-change"
			className="border-chart-warning/50 bg-chart-warning/10 flex items-start gap-3 rounded-sm border px-4 py-3 text-sm"
		>
			<GitCommitHorizontal className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex flex-col gap-0.5">
				<p className="text-foreground font-medium">{t("clusters.detail.afterChangeTitle")}</p>
				<p className="text-muted-foreground text-xs">
					{t("clusters.detail.afterChange", { agent: change.agent, from: shortHash(change.from_hash), to: shortHash(change.to_hash), at })}
				</p>
			</div>
		</div>
	);
}

/** Clusters that share part of this signature; each opens in the panel. */
function RelatedClusters({ related, onOpen }: { related: readonly ClusterRelated[]; onOpen: (signature: string) => void }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="cluster-related">
			<h3 className="text-foreground text-sm font-medium">{t("clusters.detail.related")}</h3>
			<ul className="flex flex-col gap-1.5">
				{related.map((r) => (
					<li key={r.signature}>
						<button
							type="button"
							onClick={() => onOpen(r.signature)}
							data-testid={`cluster-related-${r.signature}`}
							className="hover:bg-accent focus-visible:ring-ring/50 flex w-full cursor-pointer flex-col gap-0.5 rounded-sm border px-3 py-2 text-left text-xs transition-colors focus-visible:ring-[3px] focus-visible:outline-none"
						>
							<span className="text-foreground font-medium break-words">{r.title}</span>
							<span className="text-muted-foreground">
								<span className="tabular-nums">{t("clusters.detail.relatedRuns", { count: r.count })}</span>
								{r.reason && <span> · {r.reason}</span>}
							</span>
						</button>
					</li>
				))}
			</ul>
		</div>
	);
}

/** The cluster's runs: id linking to the run page, where it ran, the agent config, outcome, time, last tool and error line. */
function ClusterRunsTable({ runs, renderRunLink }: { runs: readonly ClusterRun[]; renderRunLink: (run: ClusterRun) => React.ReactNode }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<div className="min-w-0 rounded-sm border">
			<Table data-testid="cluster-runs-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("clusters.detail.run")}</TableHead>
						<TableHead>{t("clusters.detail.where")}</TableHead>
						<TableHead>{t("clusters.detail.agent")}</TableHead>
						<TableHead>{t("clusters.detail.outcome")}</TableHead>
						<TableHead>{t("clusters.detail.at")}</TableHead>
						<TableHead>{t("clusters.detail.lastTool")}</TableHead>
						<TableHead>{t("clusters.detail.runError")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{runs.map((run) => (
						<TableRow key={run.run_id} data-testid={`cluster-run-${run.run_id}`}>
							<TableCell className="align-top">{renderRunLink(run)}</TableCell>
							<TableCell className="align-top text-xs">
								<span className="flex flex-col">
									<span className="font-mono">
										{run.suite}/{run.task}
									</span>
									<span className="text-muted-foreground">
										{run.condition} · {t("clusters.detail.arm", { arm: run.arm })} · <span className="font-mono">{run.exp_id}</span>
									</span>
								</span>
							</TableCell>
							<TableCell className="align-top text-xs">
								<span className="flex flex-col">
									<span className="font-mono">{run.agent}</span>
									{run.config_hash && (
										<span className="text-muted-foreground font-mono text-xs" title={run.config_hash}>
											{shortHash(run.config_hash)}
										</span>
									)}
								</span>
							</TableCell>
							<TableCell className="align-top">
								<OutcomeBadge outcome={run.outcome} testId={`cluster-run-outcome-${run.run_id}`} />
							</TableCell>
							<TableCell className="text-muted-foreground align-top text-xs whitespace-nowrap">
								{formatDateTime(run.at, locale) ?? "-"}
							</TableCell>
							<TableCell className="align-top text-xs">
								{run.last_tool ? (
									<span className="flex flex-col">
										<span className="font-mono">{run.last_tool}</span>
										{run.last_tool_errored && <span className="text-chart-error-ink text-xs">{t("clusters.detail.lastToolErrored")}</span>}
									</span>
								) : (
									<span className="text-muted-foreground">-</span>
								)}
							</TableCell>
							<TableCell className="max-w-sm align-top whitespace-normal">
								{run.error_text ? (
									<code
										className="text-foreground line-clamp-3 font-mono text-xs break-all whitespace-pre-wrap"
										title={run.error_text}
										data-testid={`cluster-run-error-${run.run_id}`}
									>
										{run.error_text}
									</code>
								) : (
									<span className="text-muted-foreground text-xs">-</span>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}

export interface ClusterRunLinkProps {
	runId: string;
	/** The run's pair from the grid; null when the grid does not have it, undefined while it loads. */
	pairId: string | null | undefined;
}

/** A run id linking to its matched pair on the run page, or plain text when the pair is not known. */
export function ClusterRunLink({ runId, pairId }: ClusterRunLinkProps) {
	const { t } = useTranslation("bench");
	if (pairId) {
		return (
			<Link
				to="/runs"
				search={{ pair: pairId }}
				data-testid={`cluster-run-link-${runId}`}
				className="text-primary font-mono text-xs whitespace-nowrap underline-offset-4 hover:underline"
			>
				{runId}
			</Link>
		);
	}
	return (
		<span className="flex flex-col">
			<span className="font-mono text-xs" data-testid={`cluster-run-id-${runId}`}>
				{runId}
			</span>
			{pairId === null && <span className="text-muted-foreground text-xs">{t("clusters.detail.noPair")}</span>}
		</span>
	);
}