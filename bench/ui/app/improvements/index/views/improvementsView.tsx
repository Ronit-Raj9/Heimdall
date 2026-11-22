import { PageHeader } from "@/components/shell";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { FeatureHero } from "@/components/inbox/featureHero";
import { StatusPill } from "@/components/ui/statusPill";
import { points, statusTone, usd, verdictTone } from "@/components/improvements/improve.utils";
import { ImprovementCli, NewImprovementDialog } from "@/components/improvements/newImprovementDialog";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetImprovementsQuery } from "@/lib/store/apis/improveApi";
import type { ImprovementSummary } from "@/lib/types/improve";
import { Link } from "@tanstack/react-router";
import { Plus, TrendingUp } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** /improvements: every improvement run with status, rounds, versions kept and held-out gain. */
export default function ImprovementsView() {
	const { t } = useTranslation("improve");
	const list = useGetImprovementsQuery(undefined, { pollingInterval: 10_000 });
	// Starting a run spends the organization's model keys, so only admins may (policy org(admin)).
	const admin = canUse(useSession(), "admin");
	const [creating, setCreating] = useState(false);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="improvements-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				icon={TrendingUp}
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
				<ImprovementsTable rows={list.data.improvements} admin={admin} />
			)}
		</section>
	);
}

/** The loaded list, or the empty state. Exported for tests. */
export function ImprovementsTable({ rows, admin = false }: { rows: ImprovementSummary[]; admin?: boolean }) {
	const { t } = useTranslation("improve");
	if (rows.length === 0) {
		return (
			<FeatureHero icon={TrendingUp} title={t("empty.title")} message={admin ? t("empty.messageAdmin") : t("empty.message")} testId="improvements-empty">
				<ImprovementCli testId="improvements-cli" />
			</FeatureHero>
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
					</tr>
				</thead>
				<tbody>
					{rows.map((r) => (
						<tr key={r.id} className="hover:bg-muted/40 border-b last:border-0">
							<td className="px-3 py-2">
								<Link to="/improvements/$improvementId" params={{ improvementId: r.id }} className="text-foreground font-medium hover:underline">
									{r.name}
								</Link>
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
								{r.verdict ? <StatusPill tone={verdictTone(r.verdict)}>{points(r.sealed_gain)}</StatusPill> : <span className="text-muted-foreground">-</span>}
							</td>
							<td className="px-3 py-2 text-right tabular-nums">{usd(r.total_usd)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}
