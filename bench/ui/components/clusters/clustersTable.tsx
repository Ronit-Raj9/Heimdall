import { Badge } from "@/components/ui/badge";
import { EntityLink } from "@/components/shell";
import { CopyId } from "@/components/ui/copyId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ClusterSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronRight, GitCommitHorizontal } from "lucide-react";
import { useTranslation } from "react-i18next";
import { faultLabel, formatShare, isClusterScope, shownItems } from "./clusters.utils";
import "@/app/inbox/views/uf7aI18n";
import "@/app/failures/views/ufrhI18n";

/** Task and agent links: at least 24 px tall and centred, so a hit target is never under 24 px (M7-35). Exported for tests. */
export const LINK_TARGET = "inline-flex min-h-6 items-center text-sm";

/** Outcomes a title made of the outcome alone reads as a sentence for. */
const OUTCOME_SENTENCES = ["fail", "timeout", "crash", "budget", "refusal"] as const;

/**
 * A cluster's title. A report from an older server names a cluster that has only its outcome
 * layer by the bare outcome ("fail"); that reads as a sentence here, as the server now writes it.
 */
export function clusterTitle(
	cluster: Pick<ClusterSummary, "title" | "outcome" | "layers">,
	sentence: (outcome: (typeof OUTCOME_SENTENCES)[number]) => string,
): string {
	const layers = cluster.layers ?? [];
	const bare = layers.length <= 1 && cluster.title === cluster.outcome;
	const outcome = cluster.title as (typeof OUTCOME_SENTENCES)[number];
	return bare && OUTCOME_SENTENCES.includes(outcome) ? sentence(outcome) : cluster.title;
}

/** Who a failure group points at, read from its signature text: the agent, a tool, or the infrastructure (M7-33). */
export type ClusterKind = "agent" | "tool" | "infra";

export function clusterKind(cluster: Pick<ClusterSummary, "title" | "error_text" | "layers">): ClusterKind {
	const text = [cluster.title, cluster.error_text ?? "", ...(cluster.layers ?? []).map((l) => `${l.kind} ${l.label}`)]
		.join(" ")
		.toLowerCase();
	if (/\binfra|aborted_infra|provider|rate.?limit|\b5\d\d\b|connection|reconnect|econn|dns|sandbox|container/.test(text)) return "infra";
	if (/\btool\b|last_tool|mcp|tool_error|tool call/.test(text)) return "tool";
	return "agent";
}

/**
 * A readable title from a raw error string: repeated "error:" prefixes and ids at the front are
 * dropped and the first letter is raised ("timeout: error: error: reconnecting" reads "Timeout: reconnecting").
 */
export function plainClusterTitle(raw: string): string {
	let s = raw.trim().replace(/^(?:[a-z]_[0-9a-f]{6,}\s*[:-]?\s*)+/i, "");
	s = s.replace(/(\berror:\s*)+/gi, "");
	s = s.replace(/\s{2,}/g, " ").trim();
	if (!s) return raw;
	return s.charAt(0).toUpperCase() + s.slice(1);
}

/** Groups whose title, error text, tasks or agents hold the search text (M7-13). */
export function filterClusters<T extends Pick<ClusterSummary, "title" | "error_text" | "tasks" | "agents" | "signature">>(
	clusters: readonly T[],
	q: string,
): T[] {
	const needle = q.trim().toLowerCase();
	if (!needle) return [...clusters];
	return clusters.filter((c) =>
		[c.title, c.error_text ?? "", c.signature, ...(c.tasks ?? []), ...(c.agents ?? [])].some((x) => x.toLowerCase().includes(needle)),
	);
}

/** The kind badge of a group. */
export function ClusterKindBadge({ cluster }: { cluster: Pick<ClusterSummary, "title" | "error_text" | "layers" | "signature"> }) {
	const { t } = useTranslation("uf7a");
	const kind = clusterKind(cluster);
	return (
		<Badge variant={kind === "infra" ? "warning" : "outline"} data-testid={`clusters-kind-${cluster.signature}`} data-kind={kind}>
			{t(`clusters.kind.${kind}`)}
		</Badge>
	);
}

export interface ClustersTableProps {
	/** In the server's order: largest count first. The table never re-sorts. */
	clusters: readonly ClusterSummary[];
	/** The open cluster's signature, or null. */
	selected: string | null;
	onSelect: (signature: string | null) => void;
}

/** One row per cluster: title, size, share of failures, scope, and where it was seen. */
export function ClustersTable({ clusters, selected, onSelect }: ClustersTableProps) {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("agentBoard");
	const locale = getLocale();
	const sentence = (o: (typeof OUTCOME_SENTENCES)[number]) => tb(`clusterOutcome.${o}`);
	const { t: te } = useTranslation("evaluations");
	const { t: tr } = useTranslation("ufrh");
	const fault = (value: string) =>
		faultLabel(value, tr("clusters.faultNone"), (f) => {
			const label = te(`fault.${f}` as never, { defaultValue: "" }) as string;
			return label || undefined;
		});

	return (
		<>
			<ClusterCards clusters={clusters} selected={selected} onSelect={onSelect} />
			<div className="hidden sm:block">
				<Table stickyColumns={1} data-testid="clusters-table" aria-label={t("clusters.table.label")}>
					<TableHeader>
						<TableRow>
							<TableHead>{t("clusters.table.cluster")}</TableHead>
							<TableHead className="text-right">{t("clusters.table.count")}</TableHead>
							<TableHead className="text-right">{t("clusters.table.share")}</TableHead>
							<TableHead>{t("clusters.table.scope")}</TableHead>
							<TableHead>{t("clusters.table.tasks")}</TableHead>
							<TableHead>{t("clusters.table.agents")}</TableHead>
							<TableHead>{t("clusters.table.conditions")}</TableHead>
							<TableHead className="hidden 2xl:table-cell">{t("clusters.table.experiments")}</TableHead>
							<TableHead>{t("clusters.table.lastSeen")}</TableHead>
							<TableHead />
						</TableRow>
					</TableHeader>
					<TableBody>
						{clusters.map((cluster) => {
							const open = selected === cluster.signature;
							const toggle = () => onSelect(open ? null : cluster.signature);
							return (
								<TableRow
									key={cluster.signature}
									data-testid={`clusters-row-${cluster.signature}`}
									data-state={open ? "selected" : undefined}
								>
									<TableCell className="max-w-sm min-w-64 align-top whitespace-normal">
										<div className="flex flex-col gap-0.5">
											<div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
												<ClusterKindBadge cluster={cluster} />
												<button
													type="button"
													onClick={toggle}
													aria-expanded={open}
													aria-controls="cluster-detail"
													className="text-foreground focus-visible:ring-(--focus) w-fit cursor-pointer rounded-sm text-left font-medium break-words underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
													data-testid={`clusters-title-${cluster.signature}`}
												>
													{plainClusterTitle(clusterTitle(cluster, sentence))}
												</button>
											</div>
											{plainClusterTitle(clusterTitle(cluster, sentence)) !== clusterTitle(cluster, sentence) && (
												<span
													className="text-muted-foreground font-mono text-sm leading-6 break-all"
													data-testid={`clusters-raw-${cluster.signature}`}
												>
													{clusterTitle(cluster, sentence)}
												</span>
											)}
											<CopyId
												value={cluster.signature}
												className="text-muted-foreground text-sm font-normal"
												testId={`clusters-id-${cluster.signature}`}
											/>
											{cluster.after_config_change && (
												<span
													className="text-warning-ink inline-flex items-center gap-1 text-sm font-medium"
													data-testid={`clusters-after-change-${cluster.signature}`}
												>
													<GitCommitHorizontal className="size-3" aria-hidden="true" />
													{t("clusters.table.afterChange")}
												</span>
											)}
										</div>
									</TableCell>
									<TableCell
										className="text-right align-top font-semibold tabular-nums"
										data-testid={`clusters-count-${cluster.signature}`}
									>
										{formatCount(cluster.count, locale)}
									</TableCell>
									<TableCell
										className="text-muted-foreground text-right align-top tabular-nums"
										data-testid={`clusters-share-${cluster.signature}`}
									>
										{formatShare(cluster.share, locale)}
									</TableCell>
									<TableCell className="align-top">
										<ClusterScopeBadge scope={cluster.scope} testId={`clusters-scope-${cluster.signature}`} />
									</TableCell>
									<TableCell className="align-top">
										<CompactList items={cluster.tasks} kind="task" />
									</TableCell>
									<TableCell className="align-top">
										<CompactList items={cluster.agents} kind="agent" />
									</TableCell>
									<TableCell className="align-top">
										<CompactList items={cluster.conditions} format={fault} />
									</TableCell>
									<TableCell className="hidden align-top 2xl:table-cell">
										<CompactList items={cluster.experiments} />
									</TableCell>
									<TableCell className="text-muted-foreground align-top text-sm whitespace-nowrap">
										{formatDateTime(cluster.last_seen, locale) ?? "-"}
									</TableCell>
									<TableCell className="text-right align-top">
										<button
											type="button"
											// The title button is the row's tab stop; this second toggle is for the mouse (M7-36).
											tabIndex={-1}
											aria-expanded={open}
											aria-controls="cluster-detail"
											aria-label={t(open ? "clusters.table.hideAria" : "clusters.table.showAria", { title: cluster.title })}
											onClick={toggle}
											data-testid={`clusters-show-${cluster.signature}`}
											className={cn(
												"focus-visible:ring-(--focus) inline-flex min-h-6 cursor-pointer items-center gap-1 rounded-sm border px-2 py-1 text-sm font-medium transition-colors focus-visible:ring-2",
												open ? "border-primary bg-primary/10" : "hover:bg-accent",
											)}
										>
											{open ? (
												<ChevronDown className="size-3.5" aria-hidden="true" />
											) : (
												<ChevronRight className="size-3.5" aria-hidden="true" />
											)}
											{t("clusters.table.show")}
										</button>
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			</div>
		</>
	);
}

/** Below sm: one card per group with its title, runs, share, reach and two tasks, so no column is cut at 390 px (M7-37). */
function ClusterCards({ clusters, selected, onSelect }: ClustersTableProps) {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("agentBoard");
	const locale = getLocale();
	const sentence = (o: (typeof OUTCOME_SENTENCES)[number]) => tb(`clusterOutcome.${o}`);
	return (
		<ul className="flex flex-col gap-2 sm:hidden" data-testid="clusters-cards">
			{clusters.map((cluster) => {
				const open = selected === cluster.signature;
				const { shown, more } = shownItems(cluster.tasks);
				return (
					<li
						key={cluster.signature}
						className={cn("bg-card rounded-md border p-3", open && "border-primary")}
						data-testid={`clusters-card-${cluster.signature}`}
					>
						<button
							type="button"
							onClick={() => onSelect(open ? null : cluster.signature)}
							aria-expanded={open}
							aria-controls="cluster-detail"
							className="focus-visible:ring-(--focus) flex w-full flex-col items-start gap-1 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
						>
							<span className="flex flex-wrap items-center gap-2">
								<ClusterKindBadge cluster={cluster} />
								<span className="text-sm font-medium break-words">{plainClusterTitle(clusterTitle(cluster, sentence))}</span>
							</span>
							<span className="text-muted-foreground text-sm tabular-nums">
								{t("clusters.table.count")}: {formatCount(cluster.count, locale)} · {t("clusters.table.share")}:{" "}
								{formatShare(cluster.share, locale)}
							</span>
							<ClusterScopeBadge scope={cluster.scope} />
							{shown.length > 0 && (
								<span className="text-muted-foreground font-mono text-sm leading-6 break-all">
									{shown.join(", ")}
									{more > 0 ? ` ${t("clusters.table.more", { count: more })}` : ""}
								</span>
							)}
						</button>
					</li>
				);
			})}
		</ul>
	);
}

/** How far a cluster reaches. Systemic stands out; an unknown scope shows its raw value. */
export function ClusterScopeBadge({ scope, testId }: { scope: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!isClusterScope(scope)) {
		return (
			<Badge variant="outline" data-testid={testId} data-scope={scope}>
				{scope || "-"}
			</Badge>
		);
	}
	return (
		<Badge
			variant={scope === "systemic" ? "warning" : scope === "single_run" ? "outline" : "secondary"}
			title={t(`clusters.scope.hint.${scope}`)}
			data-testid={testId}
			data-scope={scope}
		>
			{t(`clusters.scope.${scope}`)}
		</Badge>
	);
}

/**
 * The first two items in mono, then "+N more" with every item in the title. Tasks ("suite/task")
 * and agents are links to their pages.
 */
function CompactList({
	items,
	kind,
	format,
}: {
	items: readonly string[] | null | undefined;
	kind?: "task" | "agent";
	/** A label for a plain item (the Fault column); links keep the id. */
	format?: (item: string) => string;
}) {
	const { t } = useTranslation("bench");
	const { shown, more } = shownItems(items);
	if (shown.length === 0) return <span className="text-muted-foreground text-sm">-</span>;
	return (
		<ul className="flex flex-col gap-0.5" title={more > 0 ? (items ?? []).join("\n") : undefined}>
			{shown.map((item) => (
				<li key={item} className="max-w-60 truncate font-mono text-sm leading-6" title={item}>
					{format ? format(item) : <CompactItem item={item} kind={kind} />}
				</li>
			))}
			{more > 0 && <li className="text-muted-foreground text-sm leading-6">{t("clusters.table.more", { count: more })}</li>}
		</ul>
	);
}

/** One list item: a task "suite/task" or an agent id as a link, anything else as text. */
function CompactItem({ item, kind }: { item: string; kind?: "task" | "agent" }) {
	// Links are at least 24 px tall so they are easy to hit (M7-35).
	if (kind === "agent") return <EntityLink kind="agent" id={item} className={cn(LINK_TARGET, "max-w-full overflow-hidden")} />;
	const slash = item.indexOf("/");
	if (kind === "task" && slash > 0) {
		return (
			<EntityLink kind="task" id={item.slice(slash + 1)} suite={item.slice(0, slash)} className={cn(LINK_TARGET, "max-w-full overflow-hidden")}>
				{item}
			</EntityLink>
		);
	}
	return <>{item}</>;
}