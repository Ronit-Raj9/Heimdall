import { ModeBadge } from "@/components/modeBadge";
import { RunReviewsBlock } from "@/components/review/runReviewsBlock";
import { AlignedLanes } from "@/components/runs/alignedLanes";
import { ClaimVsResult, type ClaimRow } from "@/components/runs/claimVsResult";
import { RunOutput } from "@/components/runs/runOutput";
import { EntityLink } from "@/components/shell/entityLink";
import { PageHeader } from "@/components/shell/pageHeader";
import { GatewayLane } from "@/components/runs/gatewayLane";
import { IncompleteNotice } from "@/components/runs/incompleteNotice";
import { MetricStrip } from "@/components/runs/metricStrip";
import { PairPicker } from "@/components/runs/pairPicker";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useGetCatalogQuery, useGetPairQuery, useGetRunEventsQuery, useGetRunQuery } from "@/lib/store/apis/benchApi";
import type { Pair, PairResponse, PairRun } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { ArrowLeft } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
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

	return (
		<div className="flex flex-col gap-6" data-testid="runs-screen">
			<PageHeader
				breadcrumbs={
					expParam
						? [
								{ label: t("experiments.list.title"), to: "/experiments" },
								{ label: expParam, to: "/experiments/$expId", params: { expId: expParam } },
								{ label: t("runs.title") },
							]
						: [{ label: t("runs.list.title"), to: "/runs" }, { label: t("runs.title") }]
				}
				title={t("runs.title")}
				subtitle={t("runs.subtitle")}
			/>
			{pairId ? (
				<PairComparison pairId={pairId} onClear={() => void setPairId(null)} />
			) : (
				<PairPicker expParam={expParam} onPick={(id) => void setPairId(id)} />
			)}
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
	const pair = useGetPairQuery(pairId);
	const catalog = useGetCatalogQuery();
	const lanes = useRunLanes(pair.data?.runs);

	const back = (
		<Button variant="ghost" size="sm" onClick={onClear} dataTestId="runs-choose-pair" className="w-fit">
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
		<div className="flex flex-col gap-8">
			<PairHeader pair={data} back={back} />

			<Section title={t("runs.metrics.title")} description={t("runs.metrics.description")} testId="runs-metrics-section">
				<Card className="py-0 shadow-none">
					<MetricStrip
						lanes={lanes.map((lane) => ({ arm: lane.pairRun.arm, metrics: lane.pairRun.metrics }))}
						kinds={stripMetricKinds(catalog.data?.metrics)}
					/>
				</Card>
			</Section>

			<Section title={t("runs.evidence.title")} description={t("runs.evidence.description")} testId="runs-evidence-section">
				<div className="grid gap-6 xl:grid-cols-2">
					{lanes.map((lane) => (
						<Card key={lane.pairRun.arm} className="py-0 shadow-none">
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
			</Section>

			<Section title={t("runs.claim.title")} description={t("runs.claim.description")} testId="runs-claim-section">
				<ClaimVsResult rows={lanes.map(toClaimRow)} />
			</Section>

			<Section title={t("review.runs.title")} description={t("review.runs.description")} testId="runs-reviews-section">
				<div className="grid gap-6 2xl:grid-cols-2">
					{lanes.map((lane) => (
						<Card key={lane.pairRun.arm} className="py-0 shadow-none">
							<CardContent className="flex flex-col gap-2 px-4 py-4">
								<div className="text-sm font-medium">{t("runs.arm", { arm: lane.pairRun.arm })}</div>
								<RunReviewsBlock runId={lane.pairRun.run.run_id} testId={`runs-reviews-${lane.pairRun.arm}`} />
							</CardContent>
						</Card>
					))}
				</div>
			</Section>

			<Section title={t("runs.lanes.title")} description={t("runs.lanes.description")} testId="runs-lanes-section">
				<LanesBody pair={data} lanes={lanes} />
			</Section>

			<Section title={t("runs.gateway.title")} description={t("runs.gateway.description")} testId="runs-gateway-section">
				<div className="grid gap-6 2xl:grid-cols-2">
					{lanes.map((lane) => (
						<GatewayLaneBlock key={lane.pairRun.arm} lane={lane} />
					))}
				</div>
			</Section>

			<Section title={t("runs.steps.title")} description={t("runs.steps.description")} testId="runs-steps-section">
				<Tabs defaultValue={lanes[0].pairRun.arm}>
					<TabsList>
						{lanes.map((lane) => (
							<TabsTrigger key={lane.pairRun.arm} value={lane.pairRun.arm} data-testid={`runs-steps-tab-${lane.pairRun.arm}`}>
								{t("runs.arm", { arm: lane.pairRun.arm })}
							</TabsTrigger>
						))}
					</TabsList>
					{lanes.map((lane) => (
						<TabsContent key={lane.pairRun.arm} value={lane.pairRun.arm}>
							<RunRemoteCoverage runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
							{/* W9 routing policy under test */}
							<RunGatewayPolicy runId={lane.pairRun.run.run_id} />
							{/* X1 vision grading */}
							<RunVisualPanel runId={lane.pairRun.run.run_id} />
							{/* X8 voice agents */}
							<RunVoicePanel runId={lane.pairRun.run.run_id} />
							<HardwarePanel hardware={lane.pairRun.run.hardware} arm={lane.pairRun.arm} /> {/* X7: the GPUs a GPU run held */}
							<RunStepTimeline runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</TabsContent>
					))}
				</Tabs>
			</Section>

			<Section title={t("runs.agents.title")} description={t("runs.agents.description")} testId="runs-agents-section">
				<Tabs defaultValue={lanes[0].pairRun.arm}>
					<TabsList>
						{lanes.map((lane) => (
							<TabsTrigger key={lane.pairRun.arm} value={lane.pairRun.arm} data-testid={`runs-agents-tab-${lane.pairRun.arm}`}>
								{t("runs.arm", { arm: lane.pairRun.arm })}
							</TabsTrigger>
						))}
					</TabsList>
					{lanes.map((lane) => (
						<TabsContent key={lane.pairRun.arm} value={lane.pairRun.arm}>
							<RunAgentsPanel runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</TabsContent>
					))}
				</Tabs>
			</Section>

			<Section title={t("runs.output.title")} description={t("runs.output.description")} testId="runs-diff-section">
				<Tabs defaultValue={lanes[0].pairRun.arm}>
					<TabsList>
						{lanes.map((lane) => (
							<TabsTrigger key={lane.pairRun.arm} value={lane.pairRun.arm} data-testid={`runs-diff-tab-${lane.pairRun.arm}`}>
								{t("runs.arm", { arm: lane.pairRun.arm })}
							</TabsTrigger>
						))}
					</TabsList>
					{lanes.map((lane) => (
						<TabsContent key={lane.pairRun.arm} value={lane.pairRun.arm}>
							<LaneOutput lane={lane} />
						</TabsContent>
					))}
				</Tabs>
			</Section>

			{/* X4: security findings on the lines each arm's agent added, annotated in place. */}
			<Section title={t("codescan.title")} description={t("codescan.description")} testId="runs-codescan-section">
				<Tabs defaultValue={lanes[0].pairRun.arm}>
					<TabsList>
						{lanes.map((lane) => (
							<TabsTrigger key={lane.pairRun.arm} value={lane.pairRun.arm} data-testid={`runs-codescan-tab-${lane.pairRun.arm}`}>
								{t("runs.arm", { arm: lane.pairRun.arm })}
							</TabsTrigger>
						))}
					</TabsList>
					{lanes.map((lane) => (
						<TabsContent key={lane.pairRun.arm} value={lane.pairRun.arm}>
							<RunCodeScanPanel runId={lane.pairRun.run.run_id} arm={lane.pairRun.arm} />
						</TabsContent>
					))}
				</Tabs>
			</Section>

			{scenarioLanes.length > 0 && (
				<Section title={t("runs.scenario.title")} description={t("runs.scenario.description")} testId="runs-scenario-section">
					<Tabs defaultValue={scenarioLanes[0].pairRun.arm}>
						<TabsList>
							{scenarioLanes.map((lane) => (
								<TabsTrigger key={lane.pairRun.arm} value={lane.pairRun.arm} data-testid={`runs-scenario-tab-${lane.pairRun.arm}`}>
									{t("runs.arm", { arm: lane.pairRun.arm })}
								</TabsTrigger>
							))}
						</TabsList>
						{scenarioLanes.map((lane) => (
							<TabsContent key={lane.pairRun.arm} value={lane.pairRun.arm}>
								<RunScenarioEvidence detail={lane.detail.data!} arm={lane.pairRun.arm} />
							</TabsContent>
						))}
					</Tabs>
				</Section>
			)}

			{/* X2: the services beside each arm's agent and the compose services' captured logs. */}
			<ServiceLogsSection
				runs={lanes
					.filter((lane) => lane.detail.data?.services?.length)
					.map((lane) => ({ arm: lane.pairRun.arm, runId: lane.pairRun.run.run_id }))}
			/>
		</div>
	);
}

function PairHeader({ pair, back }: { pair: PairResponse; back: React.ReactNode }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col gap-2">
			{back}
			<div className="flex flex-wrap items-center gap-3">
				<h2 className="text-foreground text-lg font-semibold" data-testid="runs-pair-heading">
					{t("runs.pairHeading", { task: pair.task, condition: pair.condition, trial: pair.trial })}
				</h2>
				<ModeBadge mode={pair.mode} />
			</div>
			<p className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
				<span className="font-mono">{t("runs.pairId", { id: pair.pair_id })}</span>
				<span>
					{t("runs.experimentLabel")} <EntityLink kind="experiment" id={pair.exp_id} />
				</span>
				<span className="flex flex-wrap items-center gap-x-2">
					{pair.runs.map((run) => (
						<span key={run.run.run_id}>
							{t("runs.arm", { arm: run.arm })} <EntityLink kind="run" id={run.run.run_id} testId={`runs-pair-run-${run.arm}`} />
						</span>
					))}
				</span>
			</p>
			{pair.runs.length > MAX_LANES && (
				<p className="text-chart-warning-ink text-xs" data-testid="runs-pair-capped">
					{t("runs.lanesCapped", { shown: MAX_LANES, total: pair.runs.length })}
				</p>
			)}
		</div>
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