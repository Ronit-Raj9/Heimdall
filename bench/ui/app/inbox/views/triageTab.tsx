import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { EmptyState, EntityLink } from "@/components/shell";
import { TriageBanner } from "@/components/triage/triageBanner";
import { TriageDrillDown, TriageRunEvidence } from "@/components/triage/triageDrillDown";
import { TriageTable } from "@/components/triage/triageTable";
import { ExperimentBehaviourChecks } from "@/components/runs/behaviourChecks"; // I28 behaviour checks
import { runLocations, triageArms } from "@/components/triage/triage.utils";
import { ExperimentSelect, defaultPickerExperiment, useExperimentPicker } from "@/components/triage/experimentSelect";
import { useEvaluationNames } from "@/components/experiments/useEvaluationNames";
import { Card } from "@/components/ui/card";
import { useGetGridQuery, useGetRunQuery, useGetTriageQuery, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { FlaskConical, SearchCheck } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { firstWithFinishedRuns } from "./patternDefaults";
import "./uf7aI18n";

/** The experiment id a triage URL names: the first of a comma list (a Clusters filter carried over). */
export function triageExpId(raw: string | null): string | null {
	const first = raw?.split(",")[0]?.trim();
	return first ? first : null;
}

/**
 * The Triage tab of /inbox: ?tab=triage&exp=&pattern= (the old /triage redirects here). Observed
 * failure patterns per arm of one experiment, never root causes (docs/triage.md). With no ?exp= the
 * newest real comparison is shown (checks, practice and debug runs only with ?all=1); the picker changes it.
 */
export default function TriageTab() {
	const { t } = useTranslation("bench");
	const { t: f } = useTranslation("uf7a");
	const [expParam, setExp] = useQueryState("exp", parseAsString);
	const [, setPattern] = useQueryState("pattern", parseAsString);
	const [all, setAll] = useQueryState("all", parseAsString);
	const list = useListExperimentsQuery();
	const experiments = useMemo(() => list.data?.experiments ?? [], [list.data]);
	// Checks, practice runs and debug runs are listed only with all=1, so the tab opens on a real comparison.
	const picker = useExperimentPicker(experiments, all === "1", useEvaluationNames());
	// With no ?exp= the tab opens on the newest listed comparison that has finished runs, so it does not open empty (M7-41).
	const expId =
		defaultPickerExperiment(picker.options, triageExpId(expParam) ?? firstWithFinishedRuns(picker.options, experiments)) ?? null;

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="triage-screen">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("triage.subtitle")}</p>
			<TriageBanner />
			<div className="flex w-full min-w-0 flex-col gap-1 sm:w-auto">
				<ExperimentSelect
					picker={picker}
					value={expId}
					onChange={(next) => {
						void setExp(next);
						void setPattern(null);
					}}
					showHidden={all === "1"}
					onShowHidden={(on) => void setAll(on ? "1" : null)}
					disabled={list.isLoading || experiments.length === 0}
					testId="triage-experiment-picker"
				/>
				{expId && (
					<EntityLink kind="experiment" id={expId} className="self-start text-sm" testId="triage-experiment-link">
						{f("patterns.openComparison")}
					</EntityLink>
				)}
			</div>
			{list.isError && (
				<LoadError title={t("triage.listError")} error={list.error} onRetry={() => void list.refetch()} testId="triage-list-error" />
			)}
			{expId ? (
				<ExperimentTriage key={expId} expId={expId} />
			) : list.isLoading ? (
				<BlockSkeleton rows={6} testId="triage-loading" />
			) : (
				!list.isError && (
					<EmptyState
						icon={FlaskConical}
						message={t("triage.noExperiments")}
						action={{ label: t("triage.newExperiment"), to: "/experiments/new" }}
						testId="triage-no-experiments"
					/>
				)
			)}
			{expId && <ExperimentBehaviourChecks key={`behaviour-${expId}`} expId={expId} />}
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
			<Section title={t("triage.table.title")} description={t("triage.table.description")} testId="triage-table-section">
				{!observed ? (
					<EmptyState compact icon={SearchCheck} message={t("triage.empty")} testId="triage-empty" />
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