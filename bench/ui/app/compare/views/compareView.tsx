import "@/components/reportcard/ufrbI18n";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { runHasVoice } from "@/app/runs_/$runId/views/runTabs";
import { RunReviewsBlock } from "@/components/review/runReviewsBlock";
import { AlignedLanes } from "@/components/runs/alignedLanes";
import { StepsSideBySide } from "@/components/compare/stepsSideBySide";
import { DivergencePanel } from "@/components/runs/divergencePanel"; // I28 first divergence
import { ClaimVsResult, type ClaimRow } from "@/components/runs/claimVsResult";
import { RunOutput } from "@/components/runs/runOutput";
import { EntityLink } from "@/components/shell/entityLink";
import { PageHeader } from "@/components/shell/pageHeader";
import { GatewayLane } from "@/components/runs/gatewayLane";
import { IncompleteNotice } from "@/components/runs/incompleteNotice";
import { MetricStrip } from "@/components/runs/metricStrip";
import { PairPicker } from "@/components/runs/pairPicker";
import { CompareLaunch } from "./compareLaunch";
import { RunDiff } from "./runDiff";
import { TwoRuns } from "./twoRuns";
import { RunRemoteCoverage } from "@/components/runs/remoteCoverage";
import { RunGatewayPolicy } from "@/components/routing/gatewayPolicyCard"; // W9 routing policy under test
import { RunVisualPanel } from "@/components/visual/runVisualPanel"; // X1 vision grading
import { RunVoicePanel } from "@/components/voice/runVoicePanel"; // X8 voice agents
import { RunScenarioEvidence } from "@/components/runs/runScenario";
import { RunEvidenceStatus } from "@/components/runs/runEvidenceStatus";
import { RunStepTimeline } from "@/components/runs/stepTimeline";
import { RunAgentsPanel } from "@/components/runs/agents/agentsSection";
import { RunCodeScanPanel } from "@/components/codescan/codeScanPanel"; // X4
import { ServiceLogsSection } from "@/components/services/serviceLogs"; // X2
import { HardwarePanel } from "@/components/gpu/hardwarePanel"; // X7
import { indexEvents, stripMetricKinds } from "@/components/runs/runs.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { hasScenarioEvidence } from "@/components/scenario/scenario.utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useGetCatalogQuery, useGetExperimentQuery, useGetPairQuery, useGetRunEventsQuery, useGetRunQuery } from "@/lib/store/apis/benchApi";
import { frozenSuiteRef, type Pair, type PairResponse, type PairRun, type RunProvenance } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { ArrowLeft, FlaskConical } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { cn } from "@/lib/utils";
import { useCallback, useEffect, useMemo, useState } from "react";
import { allPractice, defaultSetupSelection, pickedRuns, SetupPicker } from "./setupPicker";
import { useTranslation } from "react-i18next";

/**
 * The pair comparison (DESIGN.md 18 screen 2): /compare?pair=<pair_id>, every arm of one matched
 * pair side by side; without a pair, a pair picker over ?exp= (the newest experiment without it).
 * The old /runs?pair= URL redirects here.
 */
export default function CompareView() {
	const { t } = useTranslation("bench");
	const [pairId, setPairId] = useQueryState("pair", parseAsString);
	const [expParam] = useQueryState("exp", parseAsString);
	// p06-1: ?a=<run>&b=<run> compares two runs of any pairs; one pair opens that pair (history replace).
	const [runA, setRunA] = useQueryState("a", parseAsString.withOptions({ history: "replace" }));
	const [runB, setRunB] = useQueryState("b", parseAsString.withOptions({ history: "replace" }));
	const expName = useExperimentName(expParam);
	const openPair = useCallback(
		(id: string) => {
			void setPairId(id, { history: "replace" });
			void setRunA(null);
			void setRunB(null);
		},
		[setPairId, setRunA, setRunB],
	);

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="runs-screen">
			<PageHeader
				breadcrumbs={
					// M1-7, M5-7: the trail starts where the sidebar points (Runs and traces), not at Comparisons.
					expParam
						? [
								{ label: t("runs.list.title"), to: "/runs" },
								{
									// M5-7: the comparison's name when the status has one, else its id.
									label: expName ?? expParam,
									to: "/experiments/$expId",
									params: { expId: expParam },
								},
								{ label: t("runs.title") },
							]
						: [{ label: t("runs.list.title"), to: "/runs" }, { label: t("runs.title") }]
				}
				title={t("runs.title")}
			/>
			{pairId ? (
				<PairComparison pairId={pairId} onClear={() => void setPairId(null)} />
			) : runA && runB ? (
				<TwoRuns a={runA} b={runB} onPair={openPair} />
			) : (
				<PickOrLaunch expParam={expParam} onPick={(id) => void setPairId(id)} />
			)}
		</div>
	);
}

/** No pair yet: pick one from the experiment's grid, or start a new comparison. */
function PickOrLaunch({ expParam, onPick }: { expParam: string | null; onPick: (id: string) => void }) {
	const { t } = useTranslation("compareUx");
	return (
		<div className="flex min-w-0 flex-col gap-8">
			{/* X-34: the picker's own Section heading is the one heading here; no outer card or icon tile. */}
			<PairPicker expParam={expParam} onPick={onPick} />
			<div className="flex min-w-0 flex-col gap-3">
				<h2 className="text-base font-semibold tracking-tight">{t("launch.newTitle")}</h2>
				<CompareLaunch />
			</div>
		</div>
	);
}

/** One arm's run detail and events. The output is fetched by its kind (RunOutput), never the diff of an answer run. */
function useRunLane(pairRun: PairRun | undefined) {
	const runId = pairRun?.run.run_id ?? skipToken;
	return {
		pairRun,
		events: useGetRunEventsQuery(runId),
		detail: useGetRunQuery(runId),
	};
}

/** The most arms one comparison shows; the pair view says so when a pair has more. */
export const MAX_LANES = 8;

/**
 * Every arm's lane. The hook runs MAX_LANES times on every render, so the hook order never
 * changes; lanes past the pair's runs skip their requests. Before this, only runs[0] and runs[1]
 * got lanes and arm C onward was dropped.
 */
function useRunLanes(runs: readonly PairRun[] | undefined): LoadedLane[] {
	const lanes: ReturnType<typeof useRunLane>[] = [];
	for (let i = 0; i < MAX_LANES; i++) {
		// eslint-disable-next-line react-hooks/rules-of-hooks -- fixed count, see above
		lanes.push(useRunLane(runs?.[i]));
	}
	return lanes.filter((lane): lane is LoadedLane => Boolean(lane.pairRun));
}

type LoadedLane = ReturnType<typeof useRunLane> & { pairRun: PairRun };

function PairComparison({ pairId, onClear }: { pairId: string; onClear: () => void }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4b");
	const { t: tr } = useTranslation("ufrb");
	const pair = useGetPairQuery(pairId);
	const catalog = useGetCatalogQuery();
	// M4-60: one setup picker drives every section; the lane hooks load the picked setups (at most MAX_LANES).
	const allRuns = pair.data?.runs;
	const [picked, setPicked] = useState<string[] | null>(null);
	useEffect(() => setPicked(null), [pairId]);
	const chosen = useMemo(
		() => (allRuns ? pickedRuns(allRuns, picked ?? defaultSetupSelection(allRuns), MAX_LANES) : undefined),
		[allRuns, picked],
	);
	const lanes = useRunLanes(chosen);

	const back = (
		<Button variant="outline" size="sm" onClick={onClear} dataTestId="runs-choose-pair" className="w-fit">
			<ArrowLeft aria-hidden="true" />
			{t("runs.choosePair")}
		</Button>
	);

	if (pair.isLoading) {
		return (
			<div className="flex flex-col gap-4">
				{back}
				<BlockSkeleton rows={8} testId="runs-pair-loading" />
			</div>
		);
	}
	if (pair.error || !pair.data) {
		return (
			<div className="flex flex-col gap-4">
				{back}
				<LoadError title={t("runs.state.loadError")} error={pair.error} onRetry={() => void pair.refetch()} testId="runs-pair-error" />
			</div>
		);
	}

	const data = pair.data;
	// Only lanes whose run recorded scenario evidence get a tab; a plain diff pair gets no section.
	const scenarioLanes = lanes.filter((lane) => lane.detail.data && hasScenarioEvidence(lane.detail.data));
	if (lanes.length === 0) {
		return (
			<div className="flex flex-col gap-4">
				{back}
				<EmptyNote testId="runs-pair-no-runs">{t("runs.noArms")}</EmptyNote>
			</div>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-8">
			<PairHeader pair={data} back={back} />

			{/* M4-55: practice runs check the benchmark's grader, not an agent. */}
			{allPractice(data.runs) && (
				<div className="bg-muted/50 flex items-start gap-2 rounded-lg border p-3 text-sm" data-testid="compare-practice-note">
					<FlaskConical className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<span>{tu("compare.practiceNote")}</span>
				</div>
			)}

			{/* Q-145: a setup whose run has no grade is named, with a way to another run of the task. */}
			{data.runs.length > 1 &&
				ungradedArms(data.runs).map((arm) => (
					<div key={arm} className="flex flex-wrap items-center gap-3 rounded-lg border p-3 text-sm" data-testid={`compare-no-graded-${arm}`}>
						<span className="min-w-0 flex-1">{tr("compare.noGraded", { arm })}</span>
						<Button variant="outline" size="sm" onClick={onClear} dataTestId={`compare-no-graded-${arm}-other`}>
							{tr("compare.pickOther")}
						</Button>
					</div>
				))}
			{/* M4-61: a pair with one graded setup says so and offers another pair. */}
			{data.runs.length === 1 && (
				<div className="flex flex-wrap items-center gap-3 rounded-lg border p-3 text-sm" data-testid="compare-one-side">
					<span className="min-w-0 flex-1">{tu("compare.oneSide", { arm: data.runs[0].arm })}</span>
					<Button variant="outline" size="sm" onClick={onClear} dataTestId="compare-one-side-other">
						{tu("compare.otherRun")}
					</Button>
				</div>
			)}

			<SetupPicker arms={data.runs.map((r) => r.arm)} picked={lanes.map((l) => l.pairRun.arm)} max={MAX_LANES} onChange={setPicked} />

			<RunDiff
				lanes={lanes.map((lane) => ({
					arm: lane.pairRun.arm,
					run: lane.pairRun.run,
					metrics: lane.pairRun.metrics,
				}))}
			/>

			{/* K2: the two runs step by step, lined up to the first divergence. */}
			{lanes.length >= 2 && (
				<StepsSideBySide
					a={lanes[0].pairRun.run.run_id}
					b={lanes[1].pairRun.run.run_id}
					labelA={lanes[0].pairRun.arm}
					labelB={lanes[1].pairRun.arm}
				/>
			)}

			<Section title={t("runs.metrics.title")} description={t("runs.metrics.description")} testId="runs-metrics-section">
				<Card className="min-w-0 overflow-x-auto py-0 shadow-none">
					<MetricStrip
						lanes={lanes.map((lane) => ({
							arm: lane.pairRun.arm,
							metrics: lane.pairRun.metrics,
						}))}
						kinds={stripMetricKinds(catalog.data?.metrics)}
					/>
				</Card>
			</Section>

			{/* Q-143, Q-144: proof and human reviews fold to one line each, so nine runs do not make a 6000 px page. */}
			<DetailsDisclosure label={t("runs.evidence.title")} testId="runs-evidence-fold">
			<div className="flex min-w-0 flex-col gap-3" data-testid="runs-evidence-section">
				<div className="grid min-w-0 gap-6 xl:grid-cols-2">
					{lanes.map((lane) => (
						<Card key={lane.pairRun.arm} className="min-w-0 py-0 shadow-none">
							<CardContent className="space-y-2 px-4 py-4 text-sm">
								<div className="font-medium">{t("runs.arm", { arm: lane.pairRun.arm })}</div>
								{lane.detail.data ? (
									<RunEvidenceStatus detail={lane.detail.data} arm={lane.pairRun.arm} />
								) : (
									<BlockSkeleton rows={4} testId={`runs-evidence-${lane.pairRun.arm}-loading`} />
								)}
							</CardContent>
						</Card>
					))}
				</div>
			</div>

			</DetailsDisclosure>

			<Section title={t("runs.claim.title")} description={t("runs.claim.description")} testId="runs-claim-section">
				<ClaimVsResult rows={lanes.map(toClaimRow)} />
			</Section>

			<DetailsDisclosure label={t("review.runs.title")} testId="runs-reviews-fold">
			<div className="flex min-w-0 flex-col gap-3" data-testid="runs-reviews-section">
				<p className="text-muted-foreground text-sm">{t("review.runs.description")}</p>
				<div className="grid min-w-0 gap-6 2xl:grid-cols-2">
					{lanes.map((lane) => (
						<Card key={lane.pairRun.arm} className="min-w-0 py-0 shadow-none">
							<CardContent className="flex flex-col gap-2 px-4 py-4">
								<div className="text-sm font-medium">{t("runs.arm", { arm: lane.pairRun.arm })}</div>
								<RunReviewsBlock runId={lane.pairRun.run.run_id} testId={`runs-reviews-${lane.pairRun.arm}`} />
							</CardContent>
						</Card>
					))}
				</div>
			</div>
			</DetailsDisclosure>

			<Section title={t("runs.lanes.title")} description={t("runs.lanes.description")} testId="runs-lanes-section">
				<LanesBody pair={data} lanes={lanes} />
			</Section>

			<Section title={t("runs.gateway.title")} testId="runs-gateway-section">
				<div className="grid min-w-0 gap-6 2xl:grid-cols-2">
					{lanes.map((lane) => (
						<GatewayLaneBlock key={lane.pairRun.arm} lane={lane} />
					))}
				</div>
			</Section>

			<Section title={t("runs.steps.title")} description={t("runs.steps.description")} testId="runs-steps-section">
				<PerSetup lanes={lanes}>
					{(lane) => (
						<>
							<RunRemoteCoverage runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
							{/* W9 routing policy under test */}
							<RunGatewayPolicy runId={lane.pairRun.run.run_id} />
							{/* X1 vision grading */}
							<RunVisualPanel runId={lane.pairRun.run.run_id} />
							{/* X8 voice agents */}
							<RunVoicePanel runId={lane.pairRun.run.run_id} skip={!runHasVoice(lane.detail.data)} />
							<HardwarePanel hardware={lane.pairRun.run.hardware} arm={lane.pairRun.arm} /> {/* X7: the GPUs a GPU run held */}
							<RunStepTimeline runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</>
					)}
				</PerSetup>
			</Section>

			<Section title={t("runs.agents.title")} testId="runs-agents-section">
				<PerSetup lanes={lanes}>
					{(lane) => (
						<>
							<RunAgentsPanel runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</>
					)}
				</PerSetup>
			</Section>

			<Section title={t("runs.output.title")} testId="runs-diff-section">
				<PerSetup lanes={lanes}>
					{(lane) => (
						<>
							<LaneOutput lane={lane} />
						</>
					)}
				</PerSetup>
			</Section>

			{/* X4: security findings on the lines each arm's agent added, annotated in place. */}
			<Section title={t("codescan.title")} description={t("codescan.description")} testId="runs-codescan-section">
				<PerSetup lanes={lanes}>
					{(lane) => (
						<>
							<RunCodeScanPanel runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</>
					)}
				</PerSetup>
			</Section>

			{scenarioLanes.length > 0 && (
				<Section title={t("runs.scenario.title")} testId="runs-scenario-section">
					<PerSetup lanes={scenarioLanes}>
						{(lane) => (
							<>
								<RunScenarioEvidence detail={lane.detail.data!} arm={lane.pairRun.arm} />
							</>
						)}
					</PerSetup>
				</Section>
			)}

			{/* X2: the services beside each arm's agent and the compose services' captured logs. */}
			<ServiceLogsSection
				runs={lanes
					.filter((lane) => lane.detail.data?.services?.length)
					.map((lane) => ({
						arm: lane.pairRun.arm,
						runId: lane.pairRun.run.run_id,
					}))}
			/>
		</div>
	);
}

/** Setups whose run carries no grade yet (Q-145). Exported for tests. */
export function ungradedArms(runs: readonly { arm: string; run: { outcome?: string } }[]): string[] {
	return runs.filter((r) => !r.run.outcome || r.run.outcome === "unknown" || r.run.outcome === "pending").map((r) => r.arm);
}

/** A comparison's name from its cached status, or undefined (no id, loading, failed, no name). */
function useExperimentName(expId: string | null | undefined): string | undefined {
	const status = useGetExperimentQuery(expId ?? skipToken);
	return status.data?.name?.trim() || undefined;
}

function PairHeader({ pair, back }: { pair: PairResponse; back: React.ReactNode }) {
	const { t } = useTranslation("bench");
	const expName = useExperimentName(pair.exp_id);
	return (
		<div className="flex flex-col gap-2">
			{back}
			<div className="flex min-w-0 flex-wrap items-center gap-3">
				<h2
					className="text-foreground min-w-0 text-lg font-semibold break-words"
					data-testid="runs-pair-heading"
					title={t("runs.pairId", { id: pair.pair_id })}
				>
					<EntityLink kind="task" id={pair.task} suite={frozenSuiteRef(pair.suite, pair.suite_version)} testId="runs-pair-task" />{" "}
					{t("runs.pairHeadingAfterTask", {
						condition: pair.condition,
						trial: pair.trial,
					})}
				</h2>
			</div>
			<p className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs">
				<span>
					{/* M4-65: the comparison by name; the id stays in the link's title. */}
					{t("runs.experimentLabel")} <EntityLink kind="experiment" id={pair.exp_id}>{expName}</EntityLink>
				</span>
				<span className="flex flex-wrap items-center gap-x-2">
					{pair.runs.map((run) => (
						<span key={run.run.run_id}>
							{t("runs.arm", { arm: run.arm })}{" "}
							{/* Q-162: the agent by name; the run id stays in the link's title and target. */}
							<EntityLink kind="run" id={run.run.run_id} testId={`runs-pair-run-${run.arm}`}>
								{run.run.agent?.config_id || undefined}
							</EntityLink>{" "}
							<ArmProvenance provenance={run.provenance} note={run.provenance_note} arm={run.arm} />
						</span>
					))}
				</span>
			</p>
		</div>
	);
}

/**
 * One run's own provenance (QC2-27-06): scripted, model-played only with recorded model evidence,
 * mixed, unobserved (no recorded model call) or unavailable (its ledger could not be read). The
 * server mode badge alone cannot say this: a live server can hold scripted controls.
 */
export function ArmProvenance({ provenance, note, arm }: { provenance: RunProvenance | undefined; note?: string; arm: string }) {
	const { t } = useTranslation("bench");
	const { t: td } = useTranslation("runDebug");
	if (!provenance) return null;
	// A debug replay's calls were answered from the source run's recording (N27): not a warning.
	const warn = provenance !== "model" && provenance !== "replayed";
	return (
		<span
			className={cn(
				"rounded-sm border px-1 text-[11px] font-medium",
				warn ? "border-chart-warning/60 text-chart-warning-ink" : "text-muted-foreground",
			)}
			data-testid={`runs-pair-provenance-${arm}`}
			data-provenance={provenance}
			title={note}
		>
			{provenance === "replayed" ? td("provenance.replayed") : t(`runs.provenance.${provenance}`)}
			{note && <span className="sr-only">: {note}</span>}
		</span>
	);
}

function toClaimRow(lane: LoadedLane): ClaimRow {
	return {
		arm: lane.pairRun.arm,
		run: lane.pairRun.run,
		// RunDetail.grade is null until the run is graded.
		grade: lane.detail.data?.grade ?? undefined,
		gradeState: lane.detail.isLoading ? "loading" : lane.detail.error ? "error" : "ready",
	};
}

function LanesBody({ pair, lanes }: { pair: Pair; lanes: LoadedLane[] }) {
	const { t } = useTranslation("bench");
	const eventsByArm = Object.fromEntries(
		lanes.map((lane) => [lane.pairRun.arm, lane.events.data ? indexEvents(lane.events.data.events) : undefined]),
	);
	const incomplete = lanes.filter((lane) => lane.events.data?.complete === false);

	if (pair.alignment.length === 0) return <EmptyNote testId="runs-lanes-empty">{t("runs.lanes.empty")}</EmptyNote>;
	// The server aligns the first two counted runs; the steps show those arms and say so when there are more.
	const aligned = Object.keys(pair.alignment[0]?.events ?? {}).sort();
	return (
		<div className="flex flex-col gap-3">
			{lanes.length > aligned.length && (
				<p className="text-muted-foreground text-xs" data-testid="runs-lanes-aligned-arms">
					{t("runs.lanes.alignedArms", { arms: aligned.join(" and ") })}
				</p>
			)}
			{incomplete.map((lane) => (
				<IncompleteNotice key={lane.pairRun.arm} arm={lane.pairRun.arm} />
			))}
			{!pair.first_divergence && (
				<p className="text-muted-foreground text-xs" data-testid="runs-no-divergence">
					{t("runs.lanes.noDivergence")}
				</p>
			)}
			<DivergencePanel pairId={pair.pair_id} />
			<Card className="py-0 shadow-none">
				<AlignedLanes
					alignment={pair.alignment}
					arms={aligned.length > 0 ? aligned : lanes.map((lane) => lane.pairRun.arm)}
					eventsByArm={eventsByArm}
					firstDivergence={pair.first_divergence}
				/>
			</Card>
		</div>
	);
}

function GatewayLaneBlock({ lane }: { lane: LoadedLane }) {
	const { t } = useTranslation("bench");
	const { arm } = lane.pairRun;
	if (lane.events.isLoading) return <BlockSkeleton rows={4} testId={`gateway-lane-${arm}-loading`} />;
	if (lane.events.error || !lane.events.data) {
		return (
			<LoadError
				title={t("runs.gateway.eventsError")}
				error={lane.events.error}
				onRetry={() => void lane.events.refetch()}
				testId={`gateway-lane-${arm}-error`}
			/>
		);
	}
	return (
		<CardContent className="rounded-sm border px-4 py-4">
			<GatewayLane arm={arm} events={lane.events.data.events} complete={lane.events.data.complete} />
		</CardContent>
	);
}

function LaneOutput({ lane }: { lane: LoadedLane }) {
	const { t } = useTranslation("bench");
	const { arm } = lane.pairRun;
	if (lane.detail.isLoading) return <BlockSkeleton rows={6} testId={`diff-${arm}-loading`} />;
	if (lane.detail.error || !lane.detail.data) {
		return (
			<LoadError
				title={t("runs.output.loadError")}
				error={lane.detail.error}
				onRetry={() => void lane.detail.refetch()}
				testId={`output-${arm}-error`}
			/>
		);
	}
	return <RunOutput detail={lane.detail.data} arm={arm} />;
}
/** A section's body once per picked setup, each under a small "Setup X" heading (M4-60: no per-section tab strip). */
function PerSetup({ lanes, children }: { lanes: LoadedLane[]; children: (lane: LoadedLane) => React.ReactNode }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-6">
			{lanes.map((lane) => (
				<div key={lane.pairRun.arm} className="flex min-w-0 flex-col gap-2" data-testid={`compare-setup-block-${lane.pairRun.arm}`}>
					{lanes.length > 1 && <h3 className="text-sm font-medium">{t("runs.arm", { arm: lane.pairRun.arm })}</h3>}
					{children(lane)}
				</div>
			))}
		</div>
	);
}