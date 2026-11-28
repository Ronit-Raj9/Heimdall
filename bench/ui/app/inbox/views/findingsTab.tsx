import "./w443I18n";
import "@/components/livescore/ufl9I18n";

import { FindingDetail } from "@/components/findings/findingDetail";
import { FindingList, filterFindings } from "@/components/findings/findingList";
import { ToolbarSearch } from "@/components/ui/toolbar";
import { InvestigateDialog } from "@/components/findings/investigateDialog";
import { InvestigationList, investigationRows } from "@/components/findings/investigationList";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { ShowArchivedChip, useShowArchived } from "@/components/ui/showArchivedChip";
import { archivedCountOf, useGetFindingsWithArchivedQuery } from "@/lib/store/apis/archiveApi";
import { useGetFindingQuery, useGetInvestigationsQuery } from "@/lib/store/apis/findingsApi";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { defaultInvestigation } from "@/components/findings/findingPlans";
import "@/app/failures/views/ufrhI18n";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";

// One constant for the dialog's starting cap and the outer button's label (R7-5); re-exported for tests.
export { INVESTIGATION_START_CAP_USD } from "@/components/findings/findingPlans";

/**
 * The Findings tab of the inbox: /inbox?tab=findings&finding=. Tracked failures with their state,
 * the rerun that proves a fix, and the investigation reports a finding can be kept from.
 */
export default function FindingsTab() {
	const { t } = useTranslation("w443");
	const { t: l9 } = useTranslation("ufl9");
	// Archived findings stay out unless "Show archived" is on (?archived=1); nothing is deleted.
	const [showArchived, setShowArchived] = useShowArchived();
	const list = useGetFindingsWithArchivedQuery({ archived: showArchived ? "include" : undefined });
	const investigations = useGetInvestigationsQuery();
	const [selected, setSelected] = useQueryState("finding", parseAsString);
	const [investigating, setInvestigating] = useState(false);
	const [q, setQ] = useQueryState("q", parseAsString);
	const all = list.data?.findings ?? [];
	const findings = filterFindings(all, q ?? "");
	const listed = all.find((f) => f.id === selected) ?? null;
	// A link to one finding that the list does not hold (archived, or newer than the list) reads it by id.
	const one = useGetFindingQuery(selected ?? "", { skip: !selected || list.isLoading || listed !== null });
	const current = listed ?? (one.data && one.data.id === selected ? one.data : null);
	const noInvestigations = !investigations.isLoading && !investigations.error && investigationRows(investigations.data).length === 0;
	// Nothing tracked and nothing investigated: one empty box with the one next step inside it (M7-25).
	// With no comparison that has failed runs there is nothing to investigate: point at an evaluation, never at paid work (RFR-4).
	const { t: r } = useTranslation("ufrh");
	const experiments = useListExperimentsQuery().data?.experiments;
	const nothingToAnalyse = experiments !== undefined && defaultInvestigation(experiments) === "";
	const bothEmpty = !list.isLoading && !list.error && all.length === 0 && noInvestigations;
	const findOut = (
		<Button size="sm" variant="outline" onClick={() => setInvestigating(true)} data-testid="findings-find-out-why">
			{t("investigations.findOut")}
		</Button>
	);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="findings-tab">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<ShowArchivedChip count={archivedCountOf(list.data)} on={showArchived} onChange={setShowArchived} testId="findings-show-archived" />
			</div>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="findings-loading" />
			) : list.error ? (
				<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="findings-error" />
			) : bothEmpty && nothingToAnalyse ? (
				<EmptyState
					title={r("findings.noRunsTitle")}
					body={r("findings.noRunsBody")}
					action={{ label: r("findings.noRunsAction"), to: "/evaluate" }}
					testId="findings-empty-no-runs"
				/>
			) : bothEmpty ? (
				<EmptyState
					title={t("emptyTitle")}
					action={investigating ? undefined : { label: t("investigations.findOut"), onClick: () => setInvestigating(true) }}
					testId="findings-empty"
				/>
			) : all.length === 0 ? (
				<EmptyState compact title={t("emptyTitle")} body={t("emptyTrackedBody")} testId="findings-empty" />
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
						<EmptyState
							kind="filtered"
							compact
							body={t("noMatch")}
							action={{ label: t("clearSearch"), onClick: () => void setQ(null) }}
							testId="findings-no-match"
						/>
					) : (
						<div className="min-w-0 overflow-x-auto">
							<FindingList findings={findings} selected={selected} onSelect={(id) => void setSelected(id)} />
						</div>
					)}
				</div>
			)}
			{selected && !current && one.error ? (
				<LoadError title={l9("finding.loadError")} error={one.error} onRetry={() => void one.refetch()} testId="finding-one-error" />
			) : null}
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