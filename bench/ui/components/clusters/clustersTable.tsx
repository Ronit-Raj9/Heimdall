import { Badge } from "@/components/ui/badge";
import { CopyId } from "@/components/ui/copyId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ClusterSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronRight, GitCommitHorizontal } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatShare, isClusterScope, shownItems } from "./clusters.utils";

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
	const locale = getLocale();

	return (
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
					<TableHead>{t("clusters.table.experiments")}</TableHead>
					<TableHead>{t("clusters.table.lastSeen")}</TableHead>
					<TableHead />
				</TableRow>
			</TableHeader>
			<TableBody>
				{clusters.map((cluster) => {
					const open = selected === cluster.signature;
					const toggle = () => onSelect(open ? null : cluster.signature);
					return (
						<TableRow key={cluster.signature} data-testid={`clusters-row-${cluster.signature}`} data-state={open ? "selected" : undefined}>
							<TableCell className="max-w-md align-top whitespace-normal">
								<div className="flex flex-col gap-0.5">
									<button
										type="button"
										onClick={toggle}
										aria-expanded={open}
										aria-controls="cluster-detail"
										className="text-foreground focus-visible:ring-ring/50 w-fit cursor-pointer rounded-sm text-left font-medium break-words underline-offset-4 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
										data-testid={`clusters-title-${cluster.signature}`}
									>
										{cluster.title}
									</button>
									<CopyId value={cluster.signature} className="text-muted-foreground text-[11px]" />
									{cluster.after_config_change && (
										<span
											className="text-chart-warning-ink inline-flex items-center gap-1 text-[11px] font-medium"
											data-testid={`clusters-after-change-${cluster.signature}`}
										>
											<GitCommitHorizontal className="size-3" aria-hidden="true" />
											{t("clusters.table.afterChange")}
										</span>
									)}
								</div>
							</TableCell>
							<TableCell className="text-right align-top font-semibold tabular-nums" data-testid={`clusters-count-${cluster.signature}`}>
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
								<CompactList items={cluster.tasks} />
							</TableCell>
							<TableCell className="align-top">
								<CompactList items={cluster.agents} />
							</TableCell>
							<TableCell className="align-top">
								<CompactList items={cluster.conditions} />
							</TableCell>
							<TableCell className="align-top">
								<CompactList items={cluster.experiments} />
							</TableCell>
							<TableCell className="text-muted-foreground align-top text-xs whitespace-nowrap">
								{formatDateTime(cluster.last_seen, locale) ?? "-"}
							</TableCell>
							<TableCell className="text-right align-top">
								<button
									type="button"
									aria-expanded={open}
									aria-controls="cluster-detail"
									aria-label={t(open ? "clusters.table.hideAria" : "clusters.table.showAria", { title: cluster.title })}
									onClick={toggle}
									data-testid={`clusters-show-${cluster.signature}`}
									className={cn(
										"focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1 rounded-sm border px-2 py-1 text-xs font-medium transition-colors focus-visible:ring-[3px]",
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

/** The first two items in mono, then "+N more" with every item in the title. */
function CompactList({ items }: { items: readonly string[] | null | undefined }) {
	const { t } = useTranslation("bench");
	const { shown, more } = shownItems(items);
	if (shown.length === 0) return <span className="text-muted-foreground text-xs">-</span>;
	return (
		<ul className="flex flex-col gap-0.5" title={more > 0 ? (items ?? []).join("\n") : undefined}>
			{shown.map((item) => (
				<li key={item} className="font-mono text-[11px] break-all">
					{item}
				</li>
			))}
			{more > 0 && <li className="text-muted-foreground text-[11px]">{t("clusters.table.more", { count: more })}</li>}
		</ul>
	);
}