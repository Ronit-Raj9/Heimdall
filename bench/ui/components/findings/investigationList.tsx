import "@/app/inbox/views/w443I18n";

import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useAddFindingMutation,
	useGetInvestigationQuery,
	useGetInvestigationsQuery,
	type InvestigationSummary,
} from "@/lib/store/apis/findingsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** The rows of GET /investigations, whether the server answers a list or an object holding one. */
export function investigationRows(data: unknown): InvestigationSummary[] {
	if (Array.isArray(data)) return data as InvestigationSummary[];
	if (data && typeof data === "object") {
		const o = data as { investigations?: unknown; items?: unknown };
		if (Array.isArray(o.investigations)) return o.investigations as InvestigationSummary[];
	}
	return [];
}

function InvestigationGroups({ id }: { id: string }) {
	const { t } = useTranslation("w443");
	const report = useGetInvestigationQuery(id).data;
	const [add, state] = useAddFindingMutation();
	const [kept, setKept] = useState<number[]>([]);
	if (!report) return null;
	return (
		<div className="flex flex-col gap-2 pl-3" data-testid={`investigation-groups-${id}`}>
			<p className="text-muted-foreground text-xs">{report.note || t("investigations.note")}</p>
			{report.groups.map((g, i) => (
				<div key={i} className="flex flex-wrap items-center gap-3 text-sm">
					<span>{g.title}</span>
					<span className="text-muted-foreground text-xs">{t("investigations.runs", { count: g.runs })}</span>
					<Button
						size="sm"
						variant="outline"
						disabled={kept.includes(i) || state.isLoading}
						onClick={() =>
							void add({ source: "investigation", ref: `${id}:${i}` }).then((res) => {
								if ("data" in res) setKept((k) => [...k, i]);
							})
						}
						data-testid={`investigation-keep-${id}-${i}`}
					>
						{kept.includes(i) ? t("investigations.kept") : t("investigations.keep")}
					</Button>
				</div>
			))}
			{state.error ? (
				<p className="text-destructive text-sm" role="alert">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
		</div>
	);
}

/** The investigation reports of the organization, each with "Track this cause" per group; a load error and the first load are shown, not read as empty (M7-31). */
export function InvestigationList() {
	const { t } = useTranslation("w443");
	const list = useGetInvestigationsQuery();
	const rows = investigationRows(list.data);
	const [open, setOpen] = useState<string | null>(null);
	if (list.isLoading) return <BlockSkeleton rows={2} testId="investigations-loading" />;
	if (list.error)
		return (
			<LoadError
				title={t("investigations.loadError")}
				error={list.error}
				onRetry={() => void list.refetch()}
				testId="investigations-error"
			/>
		);
	if (rows.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="investigations-empty">
				{t("investigations.empty")}
			</p>
		);
	}
	return (
		<ul className="flex flex-col gap-2" data-testid="investigation-list">
			{rows.map((r) => (
				<li key={r.id} className="flex flex-col gap-1">
					<div className="flex flex-wrap items-center gap-2">
						<button
							type="button"
							className="text-primary text-left text-sm hover:underline"
							aria-expanded={open === r.id}
							onClick={() => setOpen(open === r.id ? null : r.id)}
						>
							{r.exp_id} {new Date(r.created_at).toLocaleDateString()}
						</button>
						<span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs" data-testid={`investigation-status-${r.id}`}>
							{r.stopped
								? t("investigations.stopped", { reason: r.stopped })
								: t("investigations.spent", {
										usd: (r.spent_usd ?? 0).toFixed(2),
									})}
						</span>
					</div>
					{open === r.id ? <InvestigationGroups id={r.id} /> : null}
				</li>
			))}
		</ul>
	);
}