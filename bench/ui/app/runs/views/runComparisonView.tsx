import { ModeBadge } from "@/components/modeBadge";
import { AlignedLanes } from "@/components/runs/alignedLanes";
import { ClaimVsResult, type ClaimRow } from "@/components/runs/claimVsResult";
import { DiffText } from "@/components/runs/diffText";
import { GatewayLane } from "@/components/runs/gatewayLane";
import { IncompleteNotice } from "@/components/runs/incompleteNotice";
import { MetricStrip } from "@/components/runs/metricStrip";
import { PairPicker } from "@/components/runs/pairPicker";
import { indexEvents, stripMetricKinds } from "@/components/runs/runs.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useGetCatalogQuery, useGetPairQuery, useGetRunDiffQuery, useGetRunEventsQuery, useGetRunQuery } from "@/lib/store/apis/benchApi";
import type { Pair, PairResponse, PairRun } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { ArrowLeft } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/** Matched-run inspector (DESIGN.md 18 screen 2): /runs?pair=<pair_id>, or a pair picker without one. */
export default function RunComparisonView() {
	const { t } = useTranslation("bench");
	const [pairId, setPairId] = useQueryState("pair", parseAsString);
	const [expParam] = useQueryState("exp", parseAsString);

	return (
		<div className="flex flex-col gap-6" data-testid="runs-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("runs.title")}</h1>
				<p className="text-muted-foreground text-sm">{t("runs.subtitle")}</p>
			</header>
			{pairId ? (
				<PairComparison pairId={pairId} onClear={() => void setPairId(null)} />
			) : (
				<PairPicker expParam={expParam} onPick={(id) => void setPairId(id)} />
			)}
		</div>
	);
}

/** One arm's run detail, events and diff. Called once per lane, so hook order is fixed. */
function useRunLane(pairRun: PairRun | undefined) {
	const runId = pairRun?.run.run_id ?? skipToken;
	return {
		pairRun,
		events: useGetRunEventsQuery(runId),
		detail: useGetRunQuery(runId),
		diff: useGetRunDiffQuery(runId),
	};
}

type LoadedLane = ReturnType<typeof useRunLane> & { pairRun: PairRun };

function PairComparison({ pairId, onClear }: { pairId: string; onClear: () => void }) {
	const { t } = useTranslation("bench");
	const pair = useGetPairQuery(pairId);
	const catalog = useGetCatalogQuery();
	const laneA = useRunLane(pair.data?.runs[0]);
	const laneB = useRunLane(pair.data?.runs[1]);
	const lanes = [laneA, laneB].filter((lane): lane is LoadedLane => Boolean(lane.pairRun));

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

			<Section title={t("runs.claim.title")} description={t("runs.claim.description")} testId="runs-claim-section">
				<ClaimVsResult rows={lanes.map(toClaimRow)} />
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

			<Section title={t("runs.diff.title")} description={t("runs.diff.description")} testId="runs-diff-section">
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
							<DiffBlock lane={lane} />
						</TabsContent>
					))}
				</Tabs>
			</Section>
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
			<p className="text-muted-foreground flex flex-wrap gap-x-4 font-mono text-xs">
				<span>{t("runs.pairId", { id: pair.pair_id })}</span>
				<span>{t("runs.experiment", { id: pair.exp_id })}</span>
			</p>
		</div>
	);
}

function toClaimRow(lane: LoadedLane): ClaimRow {
	return {
		arm: lane.pairRun.arm,
		run: lane.pairRun.run,
		grade: lane.detail.data?.grade,
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
	return (
		<div className="flex flex-col gap-3">
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
					arms={lanes.map((lane) => lane.pairRun.arm)}
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

function DiffBlock({ lane }: { lane: LoadedLane }) {
	const { t } = useTranslation("bench");
	const { arm } = lane.pairRun;
	if (lane.diff.isLoading) return <BlockSkeleton rows={6} testId={`diff-${arm}-loading`} />;
	if (lane.diff.error || !lane.diff.data) {
		return (
			<LoadError
				title={t("runs.diff.loadError")}
				error={lane.diff.error}
				onRetry={() => void lane.diff.refetch()}
				testId={`diff-${arm}-error`}
			/>
		);
	}
	return <DiffText runDiff={lane.diff.data} arm={arm} />;
}