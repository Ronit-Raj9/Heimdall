import { RunCodeScanPanel } from "@/components/codescan/codeScanPanel"; // X4
import { pageWidthClass } from "@/components/shell/pageWidth";
import { RunActions } from "@/components/experiments/runControls"; // run control: cancel or requeue one run
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { HardwarePanel } from "@/components/gpu/hardwarePanel"; // X7
import { RunReceiptPanel } from "@/components/receipts/runReceiptPanel";
import { RunReviewsBlock } from "@/components/review/runReviewsBlock";
import { RunGatewayPolicy } from "@/components/routing/gatewayPolicyCard"; // W9 routing policy under test
import { AgentsPanel } from "@/components/runs/agents/agentsSection";
import { ClaimVsResult } from "@/components/runs/claimVsResult";
import { GatewayLane } from "@/components/runs/gatewayLane";
import { RunThroughBifrost } from "@/components/runs/bifrost/throughBifrost"; // C09 Through Bifrost
import { MetricStrip } from "@/components/runs/metricStrip";
import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { RunRemoteCoverage } from "@/components/runs/remoteCoverage";
import { RunA2APanel } from "@/components/runs/a2a/runA2APanel";
import { RunFileDownloads } from "@/components/runs/runFileDownloads";
import { CompareWithRun } from "@/components/runs/compareWithRun";
import { RunSkillsBlock } from "@/components/runs/runSkills"; // M2 skills in this run
import { RunEvidenceStatus } from "@/components/runs/runEvidenceStatus";
import { RunOutput, runOutputKind } from "@/components/runs/runOutput";
import { ServicesList, SessionsTimeline, ToolCallTable } from "@/components/runs/runScenario";
import { stripMetricKinds, wallText } from "@/components/runs/runs.utils";
import "@/components/releases/ufk3I18n";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { runExportItems } from "@/components/runs/runExportMenu";
import { OverflowMenu, type OverflowMenuItem } from "@/components/agents/overflowMenu";
import { RunFailureCard } from "@/components/runs/runFailureCard";
import type { RunFailureFields } from "@/lib/types/serverFields";
import { failedToolCall, runReason } from "@/components/runs/runFailure.utils";
import "@/components/runs/uf5aI18n";
import { RunTraceView } from "@/components/runs/traceView";
import { RunBehaviourChecks } from "@/components/runs/behaviourChecks"; // I28 behaviour checks
import { canFork, RunDebugActions } from "@/components/runs/debug/debugActions"; // X20b run debugging
import { DebugBanner } from "@/components/runs/debug/debugBanner";
import { HoldPanel } from "@/components/runs/debug/holdPanel";
import { TraceDiff } from "@/components/runs/debug/traceDiff";
import { ScreenReplay, useRunFrames } from "@/components/runs/screenReplay"; // U08 screen replay
import { LiveScreen } from "@/components/runs/liveScreen"; // Z03 live screen and takeover
import { RecordingTab, useRunRecordings } from "@/components/runs/recordingPlayer"; // Z03 recordings
import { RunServiceLogs } from "@/components/services/serviceLogs"; // X2
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { ShareButton } from "@/components/share/shareDialog";
import { PageHeader } from "@/components/shell/pageHeader";
import { useArchiveItems } from "@/components/ui/archiveAction";
import { RouteTabs, type RouteTab } from "@/components/shell/routeTabs";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { JudgeCriteria } from "@/components/review/judgeCriteria";
import { formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { RunVisualPanel } from "@/components/visual/runVisualPanel"; // X1 vision grading
import { RunVoicePanel } from "@/components/voice/runVoicePanel"; // X8 voice agents
import i18n, { getLocale } from "@/lib/i18n";
import {
	useGetExperimentQuery,
	useGetPairQuery,
	useGetRunEventsQuery,
	useGetRunQuery,
} from "@/lib/store/apis/benchApi";
import { useGetEvaluationQuery } from "@/lib/store/apis/evaluationsApi";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { cn } from "@/lib/utils";
import { useGetRunAgentsQuery } from "@/lib/store/apis/runAgentsApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetRunVoiceQuery } from "@/lib/store/apis/voiceApi";
import type { GateResult, Mode, PairRun, RunDetail, Value } from "@/lib/types/bench";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { AlertTriangle, Check, Coins, Copy, Footprints, GitCompareArrows, RotateCcw, SearchX, Timer, Wrench, type LucideIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { RUN_TABS, hiddenRunTabs, runHasVoice, runTabFlags, type RunTabId } from "./runTabs";
import { gateName, gradeAfterCause, orderRunTabs, rerunSearch, runCrumbs } from "./runView.uf5a";
import { savedRunsSearch } from "@/components/runs/runsListSearch";
import { MaximRunButton } from "./maximRunButton";

/**
 * /runs/$runId: one run. The header names its experiment, task, arm and outcome; the tabs hold
 * everything recorded about it (?tab=), and tabs for evidence this run did not record are hidden.
 */
export default function RunView({ runId }: { runId: string }) {
	const { t } = useTranslation("bench");
	const [live, setLive] = useState(false);
	// A live (unsealed) run is polled so new steps arrive and the page flips once it seals.
	// M5-1: the same request carries the failure group ("the same failure in N runs") when the server can group it.
	const detail = useGetRunQuery({ runId, failure: true }, {
		pollingInterval: live ? 2000 : 0,
		skipPollingIfUnfocused: true,
	});
	const sealedNow = detail.data?.sealed;
	if (detail.data && live === Boolean(sealedNow)) setLive(!sealedNow);
	const agents = useGetRunAgentsQuery(runId, { skip: !detail.data });
	const voice = useGetRunVoiceQuery(runId, { skip: !runHasVoice(detail.data) });
	const { t: tReplay } = useTranslation("replay"); // U08
	const frames = useRunFrames(runId, detail.data ? !detail.data.sealed : false); // U08
	const { t: tRec } = useTranslation("recordings"); // Z03
	const recordings = useRunRecordings(runId, !detail.data?.sealed); // Z03: recordings exist once the run is exported
	const { t: tu } = useTranslation("uf5a");
	const { t: tTrace } = useTranslation("runTrace");
	const { t: tDebug } = useTranslation("runDebug");
	// Archive is a hook, so it is read before the loading and error returns.
	const archiveName = (detail.data as { task_title?: string } | undefined)?.task_title || detail.data?.slot.task || runId;
	const archive = useArchiveItems({ kind: "run", id: detail.data ? runId : "", name: archiveName, archivedAt: (detail.data as { archived_at?: string | null } | undefined)?.archived_at, testId: "run" });
	const expId = detail.data?.slot.exp_id;
	const exp = useGetExperimentQuery(expId ?? "", { skip: !expId });
	const evaluationId = exp.data?.evaluation_id;
	const evaluation = useGetEvaluationQuery(evaluationId ?? "", { skip: !evaluationId });
	// M1-8 / M3-19: the tab title names the object and the section.
	usePageTitle(
		detail.data
			? `${tu("page.titleDoc", { task: (detail.data as { task_title?: string }).task_title || detail.data.slot.task })} · ${evaluationId ? tu("page.crumbEvaluations") : tu("page.crumbRuns")} · Heimdall`
			: undefined,
	);

	// M5-18: the loading block holds about the height of the header and first panel.
	if (detail.isLoading) return <div className="min-h-[70vh]"><BlockSkeleton rows={8} testId="run-loading" /></div>;
	if (detail.error || !detail.data) {
		if (errorStatus(detail.error) === 404) {
			return (
				<EmptyState
					icon={SearchX}
					message={t("runs.page.notFound", { id: runId })}
					action={{ label: t("runs.page.backToList"), to: "/runs" }}
					testId="run-not-found"
				/>
			);
		}
		return <LoadError title={t("runs.page.loadError")} error={detail.error} onRetry={() => void detail.refetch()} testId="run-error" />;
	}

	const d = detail.data;
	const { slot, run } = d;
	// M5-17 (G1): optional names from the server; the ids stay when they are missing.
	const names = d as typeof d & { task_title?: string; suite_title?: string };
	const taskName = names.task_title || slot.task;
	const output = runOutputKind(d, d.output);
	const flags = runTabFlags(agents, frames, recordings);
	const hidden = hiddenRunTabs({
		output,
		agents: flags.agents,
		visual: Boolean(d.grade?.visual),
		voice: Boolean(voice.data),
		gpu: Boolean(run.hardware?.gpus?.length),
		services: Boolean(d.services?.length),
	});
	const tabs: RouteTab[] = RUN_TABS.map((id) => ({
		id,
		label: t(`runs.page.tabs.${id}`),
		hidden: hidden.has(id),
		testId: `run-tab-${id}`,
	}));
	if (flags.replay) tabs.splice(2, 0, { id: "replay", label: tReplay("tab"), testId: "run-tab-replay" }); // U08
	if (flags.recording) tabs.splice(2, 0, { id: "recording", label: tRec("tab"), testId: "run-tab-recording" }); // Z03
	// M5-13: the tabs every run has keep their place; tabs only some runs have come after Proof.
	const orderedTabs = orderRunTabs(tabs);
	const toolError = d.sealed && run.outcome !== "pass" ? failedToolCall(d.tool_calls) : undefined;
	const crumbs = runCrumbs({
		task: slot.task,
		expId: slot.exp_id,
		expName: exp.data?.name,
		evaluationId,
		evaluationName: evaluation.data?.spec.name,
		labels: { runs: tu("page.crumbRuns"), evaluations: tu("page.crumbEvaluations"), run: tu("page.crumbRun", { task: taskName }) },
		runsSearch: savedRunsSearch(),
	});

	const compareItem = runCompareItem(d, exp.data?.arms?.length ?? 0, { compare: t("runs.page.compare"), source: tDebug("actions.compareSource") });
	const moreItems: OverflowMenuItem[] = [
		...(d.sealed && !run.debug
			? [{ label: tu("page.rerun"), icon: RotateCcw, to: "/evaluate", search: rerunSearch(d, exp.data?.setups) as Record<string, unknown>, testId: "run-rerun" }]
			: []),
		...(compareItem ? [compareItem] : []),
		...runExportItems(runId, Boolean(d.sealed), tTrace).map((item, i) => (i === 0 ? { ...item, separatorBefore: true } : item)),
		...archive.items.map((item) => ({ ...item, separatorBefore: true })),
	];

	return (
		<div className={cn(pageWidthClass("dash"), "flex flex-col gap-6")} data-page-width="dash" data-testid="run-screen" data-run-id={runId} data-output={output}>
			<PageHeader
				breadcrumbs={crumbs}
				title={<span className="text-balance">{tu("page.title", { task: taskName, arm: slot.arm })}</span>}
				meta={
					<>
						{d.sealed ? (
							<>
								{toolError ? (
									<span
										className="bg-chart-warning/10 text-chart-warning-ink inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
										title={tu("verdict.checkInfraHint")}
										data-testid="run-outcome"
										data-outcome={run.outcome}
									>
										<AlertTriangle className="size-3" aria-hidden="true" />
										{tu("verdict.toolError")}
									</span>
								) : (
									<OutcomeBadge outcome={run.outcome} testId="run-outcome" />
								)}
								{run.human_assisted && (
									<span className="text-chart-warning-ink text-xs" title={tRec("humanAssistedNote")} data-testid="run-human-assisted">
										{tRec("humanAssisted")}
									</span>
								)}
								{run.agent.oom_killed && run.outcome === "infra_error" && (
									<span className="text-muted-foreground text-xs" data-testid="run-excluded-oom">
										{t("runs.excludedOutOfMemory", { defaultValue: "excluded: out of memory" })}
									</span>
								)}
							</>
						) : (
							<span className="text-chart-info-ink text-xs">{t("runs.list.inFlight")}</span>
						)}
					</>
				}
				subtitle={
					<span className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs [&_a]:no-underline" data-testid="run-facts">
						<span>
							{tu("page.facts", {
								name: exp.data?.name ?? slot.exp_id,
								trial: slot.trial,
								condition: slot.condition === "clean" ? tu("page.noFault") : slot.condition,
							})}
						</span>
						<span className="inline-flex items-center">
							{t("runs.page.task")}{" "}
							<span className="ml-1 inline-flex py-1">
								<EntityLink kind="task" id={slot.task} suite={slot.suite} />
							</span>
						</span>
						{names.suite_title && <span data-testid="run-suite-title">{tu("page.suite", { name: names.suite_title })}</span>}
						<CopyIdButton id={runId} />
					</span>
				}
				actions={
					<div className="flex flex-wrap items-start gap-2">
						<RunDebugActions detail={d} />
						<RunActions expId={slot.exp_id} runId={runId} sealed={Boolean(d.sealed)} outcome={d.run?.outcome} superseded={d.superseded} />
						<ShareButton kind="run_trace" target={runId} />
						<MaximRunButton runId={runId} sealed={Boolean(d.sealed)} />
						{/* Run again, Compare, the trace downloads and Archive sit behind More, so the title keeps its width. */}
						<OverflowMenu label={tu("page.more")} items={moreItems} testId="run-more" />
						{archive.alert}
					</div>
				}
			/>

			{run.debug && <DebugBanner debug={run.debug} />}

			<RouteTabs tabs={orderedTabs} defaultTab="overview" label={t("runs.page.tabsLabel")} testId="run-tabs">
				{(active) => (
					// M5-18: the panel keeps its height while a tab's own data loads, so the page below does not jump.
					<div className="min-h-[60vh]" data-testid="run-tab-panel">
						{active === "replay" ? (
							<ScreenReplay runId={runId} live={!d.sealed} />
						) : active === "recording" ? (
							<RecordingTab runId={runId} />
						) : (
							<RunTab tab={active as RunTabId} detail={d} agents={agents} />
						)}
					</div>
				)}
			</RouteTabs>
		</div>
	);
}

function RunTab({ tab, detail, agents }: { tab: RunTabId; detail: RunDetail; agents: ReturnType<typeof useGetRunAgentsQuery> }) {
	const { t } = useTranslation("bench");
	const arm = detail.slot.arm;
	const runId = detail.run.run_id;
	switch (tab) {
		case "overview":
			return <OverviewTab detail={detail} />;
		case "timeline":
			return <TimelineTab detail={detail} />;
		case "output":
			return <RunOutput detail={detail} arm={arm} kind={runOutputKind(detail, detail.output)} />;
		case "agents":
			if (agents.isError) {
				return (
					<LoadError
						title={t("runs.agents.loadError")}
						error={agents.error}
						onRetry={() => void agents.refetch()}
						testId="run-agents-error"
					/>
				);
			}
			return agents.data ? <AgentsPanel data={agents.data} arm={arm} /> : null;
		case "visual":
			return <RunVisualPanel runId={runId} />;
		case "voice":
			return <RunVoicePanel runId={runId} />;
		case "gpu":
			return <HardwarePanel hardware={detail.run.hardware} arm={arm} />;
		case "services":
			return (
				<div className="flex flex-col gap-6">
					{detail.services && <ServicesList services={detail.services} arm={arm} />}
					<RunServiceLogs runId={runId} arm={arm} />
				</div>
			);
		case "security":
			return <RunCodeScanPanel runId={runId} arm={arm} />;
		case "evidence":
			return <EvidenceTab detail={detail} />;
	}
}

/** Whether the viewport is at least lg wide (1024 px); false outside a browser. */
function isWide(): boolean {
	try {
		return typeof window !== "undefined" && window.matchMedia("(min-width: 1024px)").matches;
	} catch {
		return false;
	}
}

function OverviewTab({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("bench");
	const { t: tt } = useTranslation("runTrace");
	const { t: tu } = useTranslation("uf5a");
	const locale = getLocale();
	const { run, slot, grade } = detail;
	const exp = useGetExperimentQuery(slot.exp_id);
	const paired = Boolean(slot.pair_id) && !run.debug && (exp.data?.arms ?? []).length >= 2;
	const pair = useGetPairQuery(slot.pair_id ?? "", { skip: !paired });
	const others = (pair.data?.runs ?? []).filter((o) => o.arm !== slot.arm);
	const [factsOpen, setFactsOpen] = useState(false);
	// X-40: the run facts are open on load from lg up, folded on smaller screens.
	useEffect(() => setFactsOpen(isWide()), []);
	const toolError = detail.sealed && run.outcome !== "pass" ? failedToolCall(detail.tool_calls) : undefined;
	// M5-1: the failure group came with the run answer; only a sealed run that did not pass shows it.
	const failureGroup = detail.sealed && run.outcome !== "pass" ? (detail as RunDetail & RunFailureFields).failure : undefined;
	// M5-3: one "Verified" badge (beside the gates); the overview names the grade source only when it is not the plain verified grade.
	const plainVerified = !grade?.judged && (!detail.grade_source || detail.grade_source === "original");
	return (
		<div className="flex flex-col gap-8" data-testid="run-overview">
			{!detail.sealed && <LiveScreen runId={run.run_id} />}
			{exp.data?.decision === "invalid" && (
				<p
					className="border-chart-warning/50 bg-chart-warning/5 rounded-md border px-4 py-3 text-sm"
					role="status"
					data-testid="run-broken-comparison"
				>
					{tu("verdict.brokenComparison")}
				</p>
			)}
			<RunFailureCard run={run} detail={detail} group={failureGroup} />
			<div className="flex flex-col gap-4" data-testid="run-summary">
				{(!detail.sealed || !plainVerified) && (
					<div className="flex flex-wrap items-center gap-2">
						{!detail.sealed && (
							<span className="bg-chart-info/10 text-chart-info-ink inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium">
								<span className="bg-chart-info size-1.5 animate-pulse rounded-full" />
								{tt("overview.running")}
							</span>
						)}
						{!plainVerified && (
							<GradeSourceTag source={detail.grade_source} judged={Boolean(grade?.judged)} testId="run-grade-source" />
						)}
					</div>
				)}
				<dl className="grid grid-cols-2 gap-x-6 gap-y-3 lg:grid-cols-4" data-testid="run-stats">
					<RunStatCards detail={detail} others={others} />
				</dl>
			</div>

			<Section title={tt("overview.agentSaid")} testId="run-grade">
				<ClaimVsResult
					rows={[
						{
							arm: slot.arm,
							run,
							// R3-2: with a tool server cause, the grader's reason reads as its result, not a second cause.
							grade: gradeAfterCause(grade, runReason(detail)?.kind === "tool", (reason) => tu("claim.afterTool", { reason })),
							gradeState: "ready",
						},
					]}
				/>
				{grade?.judged && <JudgeCriteria judge={grade.judge} testId="run-judge-criteria" />}
			</Section>

			<RunBehaviourChecks runId={run.run_id} />

			<RunSkillsBlock runId={run.run_id} skills={detail.skills} />

			<details
				className="group max-w-4xl rounded-xl border"
				open={factsOpen}
				onToggle={(e) => setFactsOpen((e.currentTarget as HTMLDetailsElement).open)}
				data-testid="run-details"
			>
				<summary className="text-muted-foreground hover:text-foreground cursor-pointer px-4 py-3 text-sm font-medium">
					{tu("page.details")}
				</summary>
				<div className="flex flex-col gap-6 border-t px-4 py-4">
					<div data-testid="run-identity">
						<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
							<Fact label={t("runs.page.facts.arm")}>
								<span className="font-mono font-semibold">{slot.arm}</span>
								{!slot.counted && <span className="text-muted-foreground ml-2">{t("runs.list.notCounted")}</span>}
							</Fact>
							<Fact label={t("runs.page.facts.agent")}>
								{run.agent.config_id ? <EntityLink kind="agent" id={run.agent.config_id} /> : null}
								<span className="text-muted-foreground ml-2 font-mono">{run.agent.adapter}</span>
							</Fact>
							<Fact label={t("runs.page.facts.suite")}>
								<EntityLink kind="suite" id={slot.suite} />
							</Fact>
							<Fact label={t("runs.page.facts.attempt")}>{run.attempt}</Fact>
							<Fact label={t("runs.page.facts.started")}>{formatDateTime(run.timing.started, locale) ?? ""}</Fact>
							<Fact label={t("runs.page.facts.sealed")}>
								{run.timing.sealed ? (formatDateTime(run.timing.sealed, locale) ?? "") : t("runs.page.facts.notSealed")}
							</Fact>
							<Fact label={t("runs.page.facts.seal")}>
								<span className="font-mono" title={detail.seal_hash}>
									{detail.seal_hash ? shortHash(detail.seal_hash) : t("runs.page.facts.notSealed")}
								</span>
								{detail.seal_error && <span className="text-chart-error-ink ml-2">{t("runs.page.facts.sealMismatch")}</span>}
							</Fact>
						</dl>
					</div>
					<div className="flex flex-col gap-2" data-testid="run-metrics">
						<span className="text-sm font-medium">{tt("overview.allMetrics")}</span>
						<Card className="max-w-[40rem] py-0 shadow-none">
							<MetricStrip lanes={[{ arm: slot.arm, metrics: detail.metrics }]} kinds={stripMetricKinds(undefined)} />
						</Card>
					</div>
				</div>
			</details>

			{detail.gates.length > 0 && (
				<Section
					title={t("verdict.gates.title")}
					description={t("runs.page.gatesDescription")}
					testId="run-gates"
					actions={
						toolError ? (
							<span
								className="border-chart-warning/50 text-chart-warning-ink inline-flex h-5 items-center gap-1 rounded-[5px] border px-1.5 text-[11px] font-medium"
								title={tu("verdict.checkInfraHint")}
								data-testid="run-gates-source"
								data-grade-source="infra"
							>
								<AlertTriangle className="size-3" aria-hidden="true" />
								{tu("verdict.checkInfra")}
							</span>
						) : (
							<GradeSourceTag sealedOnly testId="run-gates-source" />
						)
					}
				>
					<RunGates gates={detail.gates} />
				</Section>
			)}

			<Section title={t("review.runs.title")} description={t("review.runs.description")} testId="run-reviews-section">
				<RunReviewsBlock runId={run.run_id} testId="run-reviews" add={{ expId: slot.exp_id, mode: (detail as { mode?: Mode }).mode }} />
			</Section>
		</div>
	);
}

/** The run's own gate results: which fired, with what effect, and the detail each recorded. */
function RunGates({ gates }: { gates: readonly GateResult[] }) {
	const { t: tu } = useTranslation("uf5a");
	const list = <RunGateList gates={gates} />;
	// M5-9: all clear reads as one line; the checks stay one click away.
	if (gates.every((g) => !g.fired)) {
		return (
			<details className="rounded-sm border text-xs" data-testid="run-gates-all-clear">
				<summary className="text-chart-success-ink cursor-pointer px-3 py-2 font-medium">
					{tu("gates.allPassed", { count: gates.length })}
					<span className="text-muted-foreground ml-2 font-normal">{tu("gates.showAll")}</span>
				</summary>
				<div className="border-t">{list}</div>
			</details>
		);
	}
	return list;
}

function RunGateList({ gates }: { gates: readonly GateResult[] }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf5a");
	return (
		<ul className="flex flex-col divide-y rounded-sm border text-xs" data-testid="run-gate-list">
			{gates.map((gate) => (
				<li
					key={gate.gate}
					className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2"
					data-testid={`run-gate-${gate.gate}`}
					data-fired={gate.fired}
				>
					<span className="font-medium">{gateName(gate.gate, tu as never)}</span>
					<span className="text-muted-foreground font-mono text-[11px]">{gate.gate}</span>
					<span className={gate.fired ? "text-chart-error-ink font-medium" : "text-chart-success-ink"}>
						{gate.fired ? t("runs.page.gateFired") : t("runs.page.gateClear")}
					</span>
					{gate.detail && <span className="text-muted-foreground min-w-0 break-words">{gate.detail}</span>}
				</li>
			))}
		</ul>
	);
}

/** A metric value as short text, or a dash when it is not known. */
function metricText(value: Value | undefined, unit?: "usd"): string {
	if (!value || typeof value.v !== "number" || value.state !== "observed")
		return unit === "usd" ? String(i18n.t("runs.page.costUnknown" as never, { ns: "bench" } as never)) : "-";
	return unit === "usd" ? `$${value.v.toFixed(value.v < 1 ? 4 : 2)}` : value.v.toLocaleString();
}

/**
 * The Overview stat cards. "Model turns" counts model turns that got an answer (the steps metric); the
 * Step by step tab also counts tool uses and commands, so the two numbers differ on purpose (N28).
 */
export function RunStatCards({ detail, others = [] }: { detail: RunDetail; others?: readonly PairRun[] }) {
	const { t: tt } = useTranslation("runTrace");
	const { t: tu } = useTranslation("uf5a");
	const { t: k3 } = useTranslation("ufk3");
	// M5-21: with the pair loaded, each number shows the other setups' value under it as a muted line.
	// Round 3 rule 9: setups with the same value share one line ("Setup B, C, D: 0"), so eight equal lines become one.
	const beside = (value: (o: PairRun) => string) => groupBeside(others.map((o) => ({ arm: o.arm, value: value(o) }))).map((g) => tu("page.setupB", { arm: g.arms.join(", "), value: g.value }));
	return (
		<>
			<StatCard
				icon={Footprints}
				label={tt("overview.steps")}
				hint={tt("overview.stepsHint")}
				value={metricText(detail.metrics.steps)}
				beside={beside((o) => metricText(o.metrics.steps))}
				testId="run-stat-steps"
			/>
			<StatCard
				icon={Wrench}
				label={tt("overview.toolCalls")}
				value={metricText(detail.metrics.tool_calls)}
				beside={beside((o) => metricText(o.metrics.tool_calls))}
				testId="run-stat-tools"
			/>
			<StatCard
				icon={Coins}
				label={tt("overview.cost")}
				value={metricText(detail.metrics.cost_usd, "usd")}
				beside={beside((o) => metricText(o.metrics.cost_usd, "usd"))}
				testId="run-stat-cost"
			/>
			{/* K3: agent time (wall_time_s, metrics/walltime.go) and the whole run from start to seal (what the runs list shows), each named. */}
			<StatCard
				icon={Timer}
				label={k3("run.agentTime")}
				hint={k3("run.agentTimeHint")}
				value={wallText(detail.metrics, detail.run.timing)}
				beside={[
					...(detail.run.timing?.sealed ? [k3("run.wholeRun", { value: wallText({}, detail.run.timing) })] : []),
					...beside((o) => wallText(o.metrics, o.run.timing)),
				]}
				testId="run-stat-time"
			/>
		</>
	);
}

/** Other setups' values grouped by value, in first-seen order. Pure. */
export function groupBeside(rows: readonly { arm: string; value: string }[]): { arms: string[]; value: string }[] {
	const out: { arms: string[]; value: string }[] = [];
	for (const row of rows) {
		const hit = out.find((g) => g.value === row.value);
		if (hit) hit.arms.push(row.arm);
		else out.push({ arms: [row.arm], value: row.value });
	}
	return out;
}

/**
 * The header's compare action (N27). "Compare Setup A and B" only for a run of an experiment with two or
 * more setups that is not a debug run (every slot has a pair id, also in one-setup experiments, so the
 * pair id alone cannot say it). A debug run instead offers "Compare with source": the side by side view
 * on its Step by step tab. Nothing shows while the experiment loads.
 */
export function RunCompareButton({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("bench");
	const { t: td } = useTranslation("runDebug");
	const { slot, run } = detail;
	const exp = useGetExperimentQuery(slot.exp_id, { skip: Boolean(run.debug) || !slot.pair_id });
	if (run.debug) {
		if (!run.debug.from_run) return null;
		return (
			<Button variant="outline" size="sm" asChild>
				<Link
					to="/runs/$runId"
					params={{ runId: run.run_id }}
					search={{ tab: "timeline" } as never}
					hash="trace-diff"
					data-testid="run-compare-source"
				>
					<GitCompareArrows aria-hidden="true" />
					{td("actions.compareSource")}
				</Link>
			</Button>
		);
	}
	if (!slot.pair_id || (exp.data?.arms ?? []).length < 2) return null;
	return (
		<Button variant="outline" size="sm" asChild>
			<Link to="/compare" search={compareSearch(slot.pair_id, slot.exp_id) as never} data-testid="run-compare">
				<GitCompareArrows aria-hidden="true" />
				{t("runs.page.compare")}
			</Link>
		</Button>
	);
}

/** The compare action as a More menu item: the same rule as RunCompareButton, or nothing. */
export function runCompareItem(detail: RunDetail, armCount: number, labels: { compare: string; source: string }): OverflowMenuItem | null {
	const { slot, run } = detail;
	if (run.debug) {
		if (!run.debug.from_run) return null;
		return { label: labels.source, icon: GitCompareArrows, to: "/runs/$runId", params: { runId: run.run_id }, search: { tab: "timeline" }, testId: "run-compare-source" };
	}
	if (!slot.pair_id || armCount < 2) return null;
	return { label: labels.compare, icon: GitCompareArrows, to: "/compare", search: compareSearch(slot.pair_id, slot.exp_id) as Record<string, unknown>, testId: "run-compare" };
}

/** One run number in the Overview strip: a plain dl item (X-40), not a card, since it does nothing on click. */
function StatCard({
	icon: Icon,
	label,
	hint,
	value,
	beside = [],
	testId,
}: {
	icon: LucideIcon;
	label: string;
	hint?: string;
	value: string;
	beside?: readonly string[];
	testId: string;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5" title={hint} data-testid={testId}>
			<dt className="text-muted-foreground flex items-center gap-1.5 text-xs">
				<Icon className="size-3.5" aria-hidden="true" />
				{label}
			</dt>
			<dd className="text-2xl font-semibold tracking-tight tabular-nums">{value}</dd>
			{beside.map((line) => (
				<dd key={line} className="text-muted-foreground text-xs tabular-nums">
					{line}
				</dd>
			))}
		</div>
	);
}

/** The run id with a copy button (M5-20); the id text stays selectable. */
function CopyIdButton({ id }: { id: string }) {
	const { t: tu } = useTranslation("uf5a");
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1400);
		return () => window.clearTimeout(timer);
	}, [copied]);
	const copy = () => {
		try {
			void navigator.clipboard?.writeText(id).then(
				() => setCopied(true),
				() => undefined,
			);
		} catch {
			// No clipboard (insecure context): the id is still selectable.
		}
	};
	return (
		<span className="inline-flex items-center gap-1">
			<span className="font-mono">{id}</span>
			<button
				type="button"
				onClick={copy}
				aria-label={copied ? tu("page.copied") : tu("page.copyId")}
				title={copied ? tu("page.copied") : tu("page.copyId")}
				className="hover:text-foreground rounded-sm p-1"
				data-testid="run-copy-id"
			>
				{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
			</button>
		</span>
	);
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="min-w-0 break-words">{children}</dd>
		</>
	);
}

/** The step a link asked for (?step=, a trace search hit, N51); undefined when absent or not a positive integer. */
export function stepParam(search: Record<string, unknown>): number | undefined {
	const raw = search.step;
	const n = typeof raw === "number" ? raw : typeof raw === "string" && /^\d+$/.test(raw) ? Number(raw) : NaN;
	return Number.isInteger(n) && n > 0 ? n : undefined;
}

function TimelineTab({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("bench");
	const arm = detail.slot.arm;
	const runId = detail.run.run_id;
	const events = useGetRunEventsQuery(runId);
	const search = useSearch({ strict: false }) as Record<string, unknown>;
	const initialStep = stepParam(search);
	const navigate = useNavigate();
	// M5-24: the trace search lives in ?tq= so a shared link or Back keeps it.
	const traceQuery = typeof search.tq === "string" ? search.tq : "";
	const setTraceQuery = (q: string) =>
		void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, tq: q || undefined })) as never, replace: true });
	return (
		<div className="flex flex-col gap-8" data-testid="run-timeline">
			<RunRemoteCoverage runId={runId} arm={arm} />
			<RunA2APanel runId={runId} />
			<RunGatewayPolicy runId={runId} />
			{!detail.sealed && <HoldPanel runId={runId} />}
			<RunTraceView
				runId={runId}
				arm={arm}
				live={!detail.sealed}
				canFork={canFork(detail)}
				initialStep={initialStep}
				query={traceQuery}
				onQueryChange={setTraceQuery}
			/>
			{detail.run.debug?.from_run && <TraceDiff runId={runId} otherRunId={detail.run.debug.from_run} />}
			{detail.sessions && detail.sessions.length > 0 && <SessionsTimeline sessions={detail.sessions} arm={arm} />}
			{detail.tool_calls && detail.tool_calls.length > 0 && <ToolCallTable calls={detail.tool_calls} arm={arm} />}
			<Section title={t("runs.gateway.title")} testId="run-gateway">
				{events.isLoading ? (
					<BlockSkeleton rows={4} testId="run-gateway-loading" />
				) : events.error || !events.data ? (
					<LoadError
						title={t("runs.gateway.eventsError")}
						error={events.error}
						onRetry={() => void events.refetch()}
						testId="run-gateway-error"
					/>
				) : (
					<CardContent className="rounded-sm border px-4 py-4">
						<GatewayLane arm={arm} events={events.data.events} complete={events.data.complete} />
					</CardContent>
				)}
			</Section>
			<ThroughBifrostSection runId={detail.run.run_id} arm={arm} />
			<Section title={t("compare.title", { ns: "ufl4" })} testId="run-compare-other">
				<CompareWithRun runId={runId} task={detail.slot.task} />
			</Section>
		</div>
	);
}

/** C09: what Bifrost did for this run's model calls, from GET /runs/{id}/bifrost. */
function ThroughBifrostSection({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("throughBifrost");
	return (
		<Section title={t("title")} testId="run-through-bifrost">
			<CardContent className="rounded-sm border px-4 py-4">
				<RunThroughBifrost runId={runId} arm={arm} />
			</CardContent>
		</Section>
	);
}

function EvidenceTab({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("bench");
	const runId = detail.run.run_id;
	return (
		<div className="flex flex-col gap-8" data-testid="run-evidence">
			<Section title={t("runs.evidence.title")} testId="run-evidence-status">
				<Card className="py-0 shadow-none">
					<CardContent className="px-4 py-4">
						<RunEvidenceStatus detail={detail} arm={detail.slot.arm} />
					</CardContent>
				</Card>
			</Section>
			{detail.receipts.length > 0 && (
				<Section title={t("receipts.title")} testId="run-receipts">
					<RunReceiptPanel runId={runId} probeId={detail.slot.condition} onClose={() => undefined} />
				</Section>
			)}
			<Section title={t("files.title", { ns: "ufl4" })} testId="run-downloads">
				<RunFileDownloads runId={runId} sealed={detail.sealed} />
			</Section>
		</div>
	);
}