import { PageHeader } from "@/components/shell";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { StatusPill } from "@/components/ui/statusPill";
import { badgeKey, points, stateTone, statusTone, usd, verdictTone } from "@/components/improvements/improve.utils";
import "@/components/improvements/w45impI18n";
import { ImprovementCli, NewImprovementDialog } from "@/components/improvements/newImprovementDialog";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetImprovementsQuery } from "@/lib/store/apis/improveApi";
import type { ImprovementSummary } from "@/lib/types/improve";
import { Link } from "@tanstack/react-router";
import { Plus, TrendingUp } from "lucide-react";
import { useEffect, useState } from "react";
import { isLiveImprovement, listPollMs } from "@/components/improvements/improvementPoll";
import { useTranslation } from "react-i18next";
import { ArchiveRowMenu } from "@/components/ui/archiveAction";
import { ARCHIVED_ROW_CLASS, ArchivedPill, ShowArchivedChip, useShowArchived, withoutArchived } from "@/components/ui/showArchivedChip";
import { archivedCountOf, archivedParam, isArchived } from "@/lib/store/apis/archiveApi";

/** A list row with the optional archive field (phase D decision 6). */
type ArchivedRow = { archived_at?: string | null };

/** /improvements: every improvement run with status, rounds, versions kept and held-out gain. */
export default function ImprovementsView() {
	const { t } = useTranslation("improve");
	// Fast polling only while a run is live (M1-62).
	const [anyLive, setAnyLive] = useState(true);
	// Archived runs are hidden unless ?archived=1 (phase D decision 6).
	const [showArchived, setShowArchived] = useShowArchived();
	const list = useGetImprovementsQuery(showArchived ? archivedParam(true) : undefined, { pollingInterval: listPollMs(anyLive), skipPollingIfUnfocused: true });
	useEffect(() => {
		if (list.data) setAnyLive(list.data.improvements.some((r) => isLiveImprovement(r.status)));
	}, [list.data]);
	// Starting a run spends the organization's model keys, so only admins may (policy org(admin)).
	const admin = canUse(useSession(), "admin");
	const [creating, setCreating] = useState(false);
	return (
		<section className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="improvements-screen">
			<PageHeader
				title={t("title")}
				actions={
					admin ? (
						<Button size="sm" onClick={() => setCreating(true)} data-testid="improvements-new">
							<Plus className="size-4" aria-hidden="true" />
							{t("create.open")}
						</Button>
					) : undefined
				}
			/>
			{!admin && (
				<p className="text-muted-foreground text-sm" data-testid="improvements-admin-only">
					{t("create.adminOnly")}
				</p>
			)}
			{creating && <NewImprovementDialog onClose={() => setCreating(false)} />}
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="improvements-loading" />
			) : list.error || !list.data ? (
				<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="improvements-error" />
			) : (
				<>
					<ShowArchivedChip
						count={
							archivedCountOf(list.data) ??
							(showArchived ? withoutArchived(list.data.improvements, (r) => isArchived(r as ArchivedRow), true).archived : undefined)
						}
						on={showArchived}
						onChange={setShowArchived}
						className="self-start"
						testId="improvements-show-archived"
					/>
					<ImprovementsTable
						rows={withoutArchived(list.data.improvements, (r) => isArchived(r as ArchivedRow), showArchived).shown}
						admin={admin}
						onCreate={() => setCreating(true)}
					/>
				</>
			)}
		</section>
	);
}

/** The loaded list, or the empty state. Exported for tests. */
export function ImprovementsTable({ rows, admin = false, onCreate }: { rows: ImprovementSummary[]; admin?: boolean; onCreate?: () => void }) {
	const { t } = useTranslation("improve");
	const { t: tw } = useTranslation("w45imp");
	const { t: ta } = useTranslation("ufd5");
	if (rows.length === 0) {
		// The shared empty state (M2-109): admins get the New improvement run action; the command
		// line stays inside its own disclosure below.
		return (
			<div className="flex flex-col items-center gap-4">
				<EmptyState
					icon={TrendingUp}
					title={t("empty.title")}
					body={admin ? t("empty.messageAdmin") : t("empty.message")}
					action={admin && onCreate ? { label: t("create.open"), onClick: onCreate } : undefined}
					testId="improvements-empty"
					className="w-full"
				/>
				<ImprovementExample />
				<ImprovementCli testId="improvements-cli" />
			</div>
		);
	}
	return (
		<div className="overflow-x-auto rounded-lg border">
			<table className="w-full text-left text-sm" data-testid="improvements-table">
				<thead className="text-muted-foreground border-b text-xs">
					<tr>
						<th className="px-3 py-2 font-medium">{t("list.name")}</th>
						<th className="px-3 py-2 font-medium">{t("list.agent")}</th>
						<th className="px-3 py-2 font-medium">{t("list.status")}</th>
						<th className="px-3 py-2 font-medium">{t("list.rounds")}</th>
						<th className="px-3 py-2 font-medium">{t("list.commits")}</th>
						<th className="px-3 py-2 font-medium">{t("list.gain")}</th>
						<th className="px-3 py-2 text-right font-medium">{t("list.spend")}</th>
						<th className="w-10 px-2 py-2">
							<span className="sr-only">{ta("archive.actions")}</span>
						</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((r) => (
						<tr key={r.id} className={`hover:bg-muted/40 border-b last:border-0 ${isArchived(r as ArchivedRow) ? ARCHIVED_ROW_CLASS : ""}`}>
							<td className="px-3 py-2">
								<Link
									to="/improvements/$improvementId"
									params={{ improvementId: r.id }}
									className="text-foreground font-medium hover:underline"
								>
									{r.name}
								</Link>
								{isArchived(r as ArchivedRow) && (
									<span className="ml-2 align-middle">
										<ArchivedPill at={(r as ArchivedRow).archived_at} testId={`improvement-archived-${r.id}`} />
									</span>
								)}
							</td>
							<td className="text-muted-foreground px-3 py-2 font-mono text-xs">{r.agent}</td>
							<td className="px-3 py-2">
								<StatusPill tone={statusTone(r.status)} pulse={r.running}>
									{t(`status.${r.status}`)}
								</StatusPill>
							</td>
							<td className="px-3 py-2 tabular-nums">{t("list.roundsValue", { done: r.rounds_done, total: r.rounds })}</td>
							<td className="px-3 py-2 tabular-nums">{r.commits}</td>
							<td className="px-3 py-2">
								<span className="flex flex-wrap items-center gap-1">
									{badgeKey(r.verdict) ? (
										<StatusPill tone={stateTone(r.verdict)} testId="improvement-badge">
											{tw(`badge.${badgeKey(r.verdict)!}`)}
										</StatusPill>
									) : r.verdict ? (
										<StatusPill tone={verdictTone(r.verdict)}>{points(r.sealed_gain)}</StatusPill>
									) : (
										<span className="text-muted-foreground">-</span>
									)}
									{badgeKey(r.verdict) && r.sealed_gain !== undefined && (
										<span className="text-muted-foreground text-xs tabular-nums">{points(r.sealed_gain)}</span>
									)}
									{r.teacher && <StatusPill tone="neutral">{tw("badge.teacher")}</StatusPill>}
									{r.status === "interrupted" && <StatusPill tone="warn">{tw("badge.interrupted")}</StatusPill>}
								</span>
							</td>
							<td className="px-3 py-2 text-right tabular-nums">{usd(r.total_usd)}</td>
							<td className="px-2 py-2 text-right">
								<ArchiveRowMenu kind="improvement" id={r.id} name={r.name} archivedAt={(r as ArchivedRow).archived_at} testId={`improvement-row-${r.id}`} />
							</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}
/**
 * A static, clearly labelled example of what an improvement run produces (X-23), so a new user
 * sees the result before spending model calls. The numbers are made up and say so.
 */
export function ImprovementExample() {
	const { t } = useTranslation("ufs1");
	return (
		<figure className="bg-muted/50 flex w-full max-w-md flex-col gap-1.5 rounded-lg border border-dashed p-3 text-left text-sm" data-testid="improvements-example">
			<figcaption className="text-muted-foreground text-xs font-semibold">{t("improveExample.title")}</figcaption>
			<p className="tabular-nums">{t("improveExample.rounds")}</p>
			<p className="text-success-ink">{t("improveExample.kept")}</p>
			<p className="text-muted-foreground">{t("improveExample.rejected")}</p>
		</figure>
	);
}
