import "./w443I18n";

import { FindingDetail } from "@/components/findings/findingDetail";
import { FindingList } from "@/components/findings/findingList";
import { InvestigateDialog } from "@/components/findings/investigateDialog";
import { InvestigationList } from "@/components/findings/investigationList";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetFindingsQuery } from "@/lib/store/apis/findingsApi";
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
	const [selected, setSelected] = useQueryState("finding", parseAsString);
	const [investigating, setInvestigating] = useState(false);
	const findings = list.data?.findings ?? [];
	const current = findings.find((f) => f.id === selected) ?? null;

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="findings-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="findings-loading" />
			) : list.error ? (
				<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="findings-error" />
			) : findings.length === 0 ? (
				<EmptyNote testId="findings-empty">{t("empty")}</EmptyNote>
			) : (
				<FindingList findings={findings} selected={selected} onSelect={(id) => void setSelected(id)} />
			)}
			{current ? <FindingDetail key={current.id} finding={current} onClose={() => void setSelected(null)} /> : null}
			<div className="flex flex-col gap-3" data-testid="findings-investigations">
				<div className="flex items-center gap-3">
					<h3 className="text-sm font-semibold">{t("investigations.title")}</h3>
					{!investigating ? (
						<Button size="sm" variant="outline" onClick={() => setInvestigating(true)} data-testid="findings-find-out-why">
							{t("investigations.findOut")}
						</Button>
					) : null}
				</div>
				{investigating ? <InvestigateDialog onDone={() => setInvestigating(false)} /> : null}
				<InvestigationList />
			</div>
		</section>
	);
}