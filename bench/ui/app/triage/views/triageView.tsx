import { useExperimentId } from "@/components/experiments/useExperimentId";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { TriageBanner } from "@/components/triage/triageBanner";
import { TriageDrillDown, TriageRunEvidence } from "@/components/triage/triageDrillDown";
import { TriageTable } from "@/components/triage/triageTable";
import { runLocations, triageArms } from "@/components/triage/triage.utils";
import { Card } from "@/components/ui/card";
import { useGetGridQuery, useGetRunQuery, useGetTriageQuery } from "@/lib/store/apis/benchApi";
import { parseAsString, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/** Failure triage: /triage?exp=&pattern=. Observed patterns per arm, never root causes (docs/triage.md). */
export default function TriageView() {
	const { t } = useTranslation("bench");
	const [expParam] = useQueryState("exp", parseAsString);
	const experiment = useExperimentId(expParam);

	return (
		<div className="flex flex-col gap-6" data-testid="triage-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("triage.title")}</h1>
				<p className="text-muted-foreground text-sm">{t("triage.subtitle")}</p>
			</header>
			<TriageBanner />
			{experiment.isLoading ? (
				<BlockSkeleton rows={6} testId="triage-loading" />
			) : experiment.error ? (
				<LoadError title={t("triage.listError")} error={experiment.error} onRetry={experiment.refetch} testId="triage-list-error" />
			) : !experiment.expId ? (
				<EmptyNote testId="triage-no-experiments">{t("triage.noExperiments")}</EmptyNote>
			) : (
				<ExperimentTriage expId={experiment.expId} />
			)}
		</div>
	);
}

function ExperimentTriage({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const [patternParam, setPattern] = useQueryState("pattern", parseAsString);
	const triage = useGetTriageQuery(expId);
	const grid = useGetGridQuery(expId);
	const locations = useMemo(() => runLocations(grid.data), [grid.data]);

	if (triage.isLoading) return <BlockSkeleton rows={6} testId="triage-table-loading" />;
	if (triage.error || !triage.data) {
		return <LoadError title={t("triage.loadError")} error={triage.error} onRetry={() => void triage.refetch()} testId="triage-error" />;
	}

	const groups = triage.data.groups;
	const arms = triageArms(groups);
	const observed = groups.some((g) => Object.values(g.per_arm).some((r) => r.den + r.unknown > 0));
	const selected = groups.find((g) => g.pattern === patternParam) ?? null;

	return (
		<div className="flex flex-col gap-6">
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-muted-foreground font-mono text-xs">{t("triage.experiment", { id: expId })}</span>
				<ModeBadge mode={triage.data.mode} />
			</div>
			<Section title={t("triage.table.title")} description={t("triage.table.description")} testId="triage-table-section">
				{!observed ? (
					<EmptyNote testId="triage-empty">{t("triage.empty")}</EmptyNote>
				) : (
					<Card className="py-0 shadow-none">
						<TriageTable groups={groups} arms={arms} selected={selected?.pattern ?? null} onSelect={(p) => void setPattern(p)} />
					</Card>
				)}
			</Section>
			{selected && (
				<Card className="px-4 py-4 shadow-none">
					<TriageDrillDown
						group={selected}
						arms={arms}
						locations={locations}
						onClose={() => void setPattern(null)}
						renderEvidence={(runId) => <RunEvidence runId={runId} pattern={selected.pattern} />}
					/>
				</Card>
			)}
		</div>
	);
}

/** Loads one run's metrics and shows the pattern's triggering events. */
function RunEvidence({ runId, pattern }: { runId: string; pattern: string }) {
	const run = useGetRunQuery(runId);
	return <TriageRunEvidence runId={runId} value={run.data?.metrics[pattern]} isLoading={run.isLoading} isError={Boolean(run.error)} />;
}