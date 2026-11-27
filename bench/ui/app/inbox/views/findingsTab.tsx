import "./w443I18n";

import { FindingDetail } from "@/components/findings/findingDetail";
import { FindingList, filterFindings } from "@/components/findings/findingList";
import { ToolbarSearch } from "@/components/ui/toolbar";
import { InvestigateDialog } from "@/components/findings/investigateDialog";
import { InvestigationList, investigationRows } from "@/components/findings/investigationList";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetFindingsQuery, useGetInvestigationsQuery } from "@/lib/store/apis/findingsApi";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * The Findings tab of the inbox: /inbox?tab=findings&finding=. Tracked failures with their state,
 * the rerun that proves a fix, and the investigation reports a finding can be kept from.
 */
export default function FindingsTab() {
	const { t } = useTranslation("w443");
	const list = useGetFindingsQuery();
	const investigations = useGetInvestigationsQuery();
	const [selected, setSelected] = useQueryState("finding", parseAsString);
	const [investigating, setInvestigating] = useState(false);
	const [q, setQ] = useQueryState("q", parseAsString);
	const all = list.data?.findings ?? [];
	const findings = filterFindings(all, q ?? "");
	const current = all.find((f) => f.id === selected) ?? null;
	const noInvestigations = !investigations.isLoading && !investigations.error && investigationRows(investigations.data).length === 0;
	// Nothing tracked and nothing investigated: one empty box with the one next step inside it (M7-25).
	const bothEmpty = !list.isLoading && !list.error && all.length === 0 && noInvestigations;
	const findOut = (
		<Button
			size="sm"
			variant={bothEmpty ? "default" : "outline"}
			onClick={() => setInvestigating(true)}
			data-testid="findings-find-out-why"
		>
			{t("investigations.findOutPaid")}
		</Button>
	);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="findings-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="findings-loading" />
			) : list.error ? (
				<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="findings-error" />
			) : bothEmpty ? (
				<EmptyNote testId="findings-empty">
					<span className="flex flex-col items-start gap-3">
						<span>{t("empty")}</span>
						{!investigating && findOut}
					</span>
				</EmptyNote>
			) : all.length === 0 ? (
				<EmptyNote testId="findings-empty">{t("emptyTracked")}</EmptyNote>
			) : (
				<div className="flex min-w-0 flex-col gap-3">
					<ToolbarSearch
						value={q ?? ""}
						onChange={(next) => void setQ(next || null)}
						label={t("search")}
						placeholder={t("search")}
						testId="findings-search"
					/>
					{findings.length === 0 ? (
						<EmptyNote testId="findings-no-match">{t("noMatch")}</EmptyNote>
					) : (
						<div className="min-w-0 overflow-x-auto">
							<FindingList findings={findings} selected={selected} onSelect={(id) => void setSelected(id)} />
						</div>
					)}
				</div>
			)}
			{current ? <FindingDetail key={current.id} finding={current} onClose={() => void setSelected(null)} /> : null}
			{bothEmpty && investigating ? <InvestigateDialog onDone={() => setInvestigating(false)} /> : null}
			{!bothEmpty && (
				<div className="flex flex-col gap-3" data-testid="findings-investigations">
					<div className="flex items-center gap-3">
						<h2 className="text-base font-semibold">{t("investigations.title")}</h2>
						{!investigating ? findOut : null}
					</div>
					{investigating ? <InvestigateDialog onDone={() => setInvestigating(false)} /> : null}
					<InvestigationList />
				</div>
			)}
		</section>
	);
}