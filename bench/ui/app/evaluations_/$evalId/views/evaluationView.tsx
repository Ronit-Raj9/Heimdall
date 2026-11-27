import { pageWidthClass } from "@/components/shell/pageWidth";
import { cn } from "@/lib/utils";
import { EvaluationRerunNotice, EvaluationRunActions } from "@/components/experiments/runControls"; // run control: pause, resume, rerun failures
import "./fw08I18n";
import { EvaluationKindLabel, EvaluationStatusPill } from "@/app/evaluations/views/evaluationWords";
import "@/components/reportcard/reportCardI18n";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { ChevronRight, ServerCrash } from "lucide-react";
import { EvaluationLivePill } from "@/app/evaluations/views/evaluationLivePill";
import {
	EVALUATION_POLL_MS,
	armAgentLabel,
	childReasons,
	childRunsText,
	didNotStart,
	evaluationBudget,
	evaluationSpend,
	failureCause,
	type ChildWithCause,
	formatCreated,
	isActiveStatus,
	splitChildren,
} from "@/app/evaluations/views/evaluation.utils";
import { useConfirm } from "@/components/platform/confirmDialog";
import { EvaluationReportCard } from "@/components/reportcard/evaluationReportCard";
import { ModelSwitchReportView } from "@/components/modelswitch/modelSwitchReport"; // Z21 model switch report
import { GateResultCard, PromoteEvaluation, RepeatEvaluation } from "@/components/releases/promoteEvaluation";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { EvaluationBifrostSection } from "@/components/bifrost/bifrostUse";
import { ShareButton } from "@/components/share/shareDialog";
import { PageHeader } from "@/components/shell/pageHeader";
import { ArchiveButton } from "@/components/ui/archiveAction";
import { Button } from "@/components/ui/button";
import { DropdownMenu } from "@/components/ui/dropdownMenu";
import { useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import {
	useCancelEvaluationMutation,
	useGetEvaluationQuery,
	type Evaluation,
	type EvaluationArm,
	type EvaluationChild,
} from "@/lib/store/apis/evaluationsApi";
import { Link, useNavigate } from "@tanstack/react-router";
import { DeleteAction } from "@/components/platform/deleteAction";
import { useDeleteEvaluationMutation } from "@/lib/store/apis/dataOpsApi";
import { useState } from "react";
import { childStoppedText, RAISE_BUDGET_ANCHOR, RaiseBudgetPanel } from "./raiseBudget";
import { useTranslation } from "react-i18next";
import { FirstLookNote } from "@/app/connect/views/firstLookLauncher"; // lane Z01: first look is too small for a verdict
import { Badge } from "@/components/ui/badge";
import { formatCost } from "@/components/leaderboard/agentBoard.utils";
import { parseSkillRefusal, SkillScanProblem } from "./skillScanProblem";
import "./w4m2I18n";
import "@/components/reportcard/ufrbI18n";
import { evaluationTotals } from "@/components/reportcard/pageNumbers";
import { useGetEvaluationReportQuery, type Report } from "@/lib/store/apis/evaluationsApi";
import { useGetEvaluationReleasePreviewQuery } from "@/lib/store/apis/evalReleaseApi";
import { ReleaseReportContext, versionName } from "@/components/releases/releaseEvidence";

/** Fields the server adds to an evaluation in fix wave 3 (N182, N183), read here before the shared type has them. */
type EvaluationLaunchFacts = Evaluation & { launch?: { started: number; of: number; current?: string }; problem?: string };

/** "Starting benchmark 2 of 3: bfcl...": the benchmark the background launch is on, or null once every one started. */
function startingLine(evaluation: Evaluation): { n: number; m: number; name?: string } | null {
	const launch = (evaluation as EvaluationLaunchFacts).launch;
	if (!launch || launch.of <= 0 || launch.started >= launch.of) return null;
	return { n: Math.min(launch.started + 1, launch.of), m: launch.of, name: launch.current || undefined };
}

/** /evaluations/$evalId: live progress per benchmark, then the report card. */
export default function EvaluationView({ evalId }: { evalId: string }) {
	const { t } = useTranslation("evaluations");
	const first = useGetEvaluationQuery(evalId);
	const active = first.data ? isActiveStatus(first.data.status) : false;
	// Poll every 3 s while the evaluation is still moving; RTK Query shares the cache entry.
	useGetEvaluationQuery(evalId, { pollingInterval: active ? EVALUATION_POLL_MS : 0, skipPollingIfUnfocused: true, skip: !active });
	if (first.isLoading) return <BlockSkeleton rows={6} testId="evaluation-loading" />;
	if ((first.error as { status?: unknown } | undefined)?.status === 404) {
		// A wrong id is not a load failure: no Retry, a way back to the list (M3-20).
		return (
			<div className="flex flex-col items-start gap-3 rounded-xl border p-6" data-testid="evaluation-not-found">
				<p className="text-sm font-medium">{t("detail.notFound", { id: evalId })}</p>
				<Link to="/evaluations" className="text-primary text-sm font-medium hover:underline">
					{t("detail.allEvaluations")}
				</Link>
			</div>
		);
	}
	if (first.error || !first.data) {
		return (
			<LoadError
				title={t("detail.loadError", { id: evalId })}
				error={first.error}
				onRetry={() => void first.refetch()}
				testId="evaluation-error"
			/>
		);
	}
	return <EvaluationBody evaluation={first.data} />;
}

/** The loaded evaluation. Exported for tests. */
/**
 * What the progress section says before any benchmark has a row: "starting" while the evaluation is still
 * active and nothing was skipped, "none" when no benchmark could run, null once a benchmark runs.
 */
export function benchmarksNote(run: number, skipped: number, active: boolean): "starting" | "none" | null {
	if (run > 0) return null;
	if (active && skipped === 0) return "starting";
	return "none";
}

/** "3 tasks per benchmark, each tried once", with plurals (namespace w4m2). */
export function setupLine(t: (key: string, opts?: Record<string, unknown>) => string, tasks: number, trials: number): string {
	return [t("setupTasks", { ns: "w4m2", count: tasks }), t("setupTrials", { ns: "w4m2", count: trials })].join(", ");
}

/** Queue facts the server may add to a queued evaluation (M3-21); absent on the current server. */
type QueueFacts = { queue?: { workers_online?: number; image_preparing?: boolean; ahead?: number } };

/** Why a queued evaluation waits, from the server's queue facts, or null when it does not say (M3-21). */
export function queuedReason(evaluation: Evaluation): { key: "noWorker" | "image" | "queue"; opts?: Record<string, unknown> } | null {
	const q = (evaluation as Evaluation & QueueFacts).queue;
	if (!q) return null;
	if (q.workers_online === 0) return { key: "noWorker" };
	if (q.image_preparing) return { key: "image" };
	if (typeof q.ahead === "number" && q.ahead > 0) return { key: "queue", opts: { n: q.ahead } };
	return null;
}

/**
 * The subtitle by status (M3-5): progress while running, date, runs and spend once finished. The run count and the
 * spend come from evaluationTotals, the same helper as the report card's cost line (Q-15): every run, normal and with
 * faults, and every dollar the benchmarks spent. A stored result's time is when it was scored, so it says "Scored".
 */
export function evaluationSubtitle(
	t: (key: string, opts?: Record<string, unknown>) => string,
	evaluation: Evaluation,
	locale?: string,
	report?: Report,
	tu: (key: string, opts?: Record<string, unknown>) => string = t,
): string {
	const date = formatCreated(evaluation.result?.at || evaluation.created_at, locale);
	if (isActiveStatus(evaluation.status)) return t("detail.subtitleRunning");
	if (didNotStart(evaluation)) return t("detail.subtitleFailed");
	if (evaluation.status === "cancelled") return t("detail.subtitleCancelled", { date });
	if (evaluation.status === "done") {
		const totals = evaluationTotals(evaluation, report);
		const budget = evaluationBudget(evaluation);
		const spent = formatCost(totals.costUsd ?? 0);
		const runs = totals.runs;
		if (typeof runs !== "number") {
			return budget > 0
				? tu("subtitle.scoredNoRuns", { ns: "ufrb", date, spent, budget: formatCost(budget) })
				: tu("subtitle.scoredOnlyDate", { ns: "ufrb", date });
		}
		return budget > 0
			? tu("subtitle.scored", { ns: "ufrb", date, runs, spent, budget: formatCost(budget) })
			: tu("subtitle.scoredNoBudget", { ns: "ufrb", date, runs });
	}
	return t("detail.subtitleRunning");
}

/** "$0.0098 of $5.00 spent" for one benchmark with a cap, money rounded as everywhere on the page (formatCost). */
export function childSpendLine(c: EvaluationChild): string | undefined {
	if (!c.budget_usd) return undefined;
	return `${formatCost(c.spent_usd ?? 0)} of ${formatCost(c.budget_usd)} spent`;
}

/** The wizard link that starts the same evaluation again (M3-9): agent, mode and first benchmark in the URL. */
export function runAgainSearch(evaluation: Evaluation): Record<string, string> {
	const arm = evaluation.spec.arms?.[0];
	const out: Record<string, string> = {};
	if (arm?.agent) out.agent = arm.agent;
	if (evaluation.spec.mode) out.mode = String(evaluation.spec.mode);
	const suite = evaluation.spec.benchmarks?.[0];
	if (suite) out.suite = suite;
	return out;
}

/** A raw worker message as one plain sentence when its cause is known, the raw text inside "Technical details" (M3-9). */
function ProblemText({ raw, benchmark, testId, code }: { raw: string; benchmark: string; testId?: string; code?: string }) {
	const { t } = useTranslation("evaluations");
	const cause = failureCause(raw, code);
	const [copied, setCopied] = useState(false);
	if (!cause) return <>{testId ? <span data-testid={testId}>{t("detail.childProblem", { problem: raw })}</span> : raw}</>;
	return (
		<span className="flex min-w-0 flex-col gap-1" data-testid={testId}>
			<span className="min-w-0 break-words">
				{cause ? t(`failure.${cause}` as never, { benchmark }) : t("detail.childProblem", { problem: raw })}
			</span>
			{cause && (
				<DetailsDisclosure label={t("detail.technical")} className="text-xs">
					<span className="flex flex-col items-start gap-2">
						<code className="text-muted-foreground font-mono text-xs break-all whitespace-pre-wrap">{raw}</code>
						<button
							type="button"
							className="text-primary text-xs font-medium hover:underline"
							onClick={() => {
								void navigator.clipboard?.writeText(raw).then(() => setCopied(true));
							}}
						>
							{copied ? t("detail.copied") : t("detail.copy")}
						</button>
					</span>
				</DetailsDisclosure>
			)}
		</span>
	);
}

/** The items of the header "More" menu: schedules (finished only), archive and delete (M3-11). */
export function EvaluationMoreItems({ evaluation, name }: { evaluation: Evaluation; name: string }) {
	const done = evaluation.status === "done";
	return (
		<>
			{done && <RepeatEvaluation evaluationId={evaluation.id} />}
			<ArchiveButton kind="evaluation" id={evaluation.id} name={name} archivedAt={(evaluation as { archived_at?: string | null }).archived_at} testId="evaluation-more" />
			<DeleteEvaluationAction id={evaluation.id} active={isActiveStatus(evaluation.status)} />
		</>
	);
}

export function EvaluationBody({ evaluation }: { evaluation: Evaluation }) {
	const { t, i18n } = useTranslation("evaluations");
	const { t: tf } = useTranslation("fw08");
	const { run, skipped } = splitChildren(evaluation.children);
	const budget = evaluationBudget(evaluation);
	const active = isActiveStatus(evaluation.status);
	const spec = evaluation.spec;
	const faults = (spec.faults ?? []).filter((f) => f !== "clean");
	const starting = startingLine(evaluation);
	const problem = (evaluation as EvaluationLaunchFacts).problem;
	const { t: tw } = useTranslation("w4m2");
	const { t: tu } = useTranslation("ufrb");
	// The same cache entry the report card reads: no second request (Q-15).
	const reportQ = useGetEvaluationReportQuery(evaluation.id, { skip: evaluation.status !== "done" });
	const note = benchmarksNote(run.length, skipped.length, active);
	const notStarted = didNotStart(evaluation);
	const done = evaluation.status === "done";
	// The gateway block only means something once a run started (M3-1).
	const anyRunStarted = !notStarted && run.length > 0;
	const name = spec.name || evaluation.id;
	usePageTitle(`${name} \u00b7 ${t("detail.crumb")} \u00b7 Heimdall`);
	const report = done && (
		<>
			{evaluation.gate_result && <GateResultCard gate={evaluation.gate_result} />}
			<EvaluationReportCard evaluationId={evaluation.id} evaluation={evaluation} />
			{(spec.mode === "versions" || spec.mode === "score") && (
				// The full release block, folded; a one line summary sits above the report (Q-61).
				<details id={RELEASE_ANCHOR} className="group/release min-w-0 scroll-mt-20" data-testid="evaluation-release-fold">
					<summary className="text-foreground hover:bg-muted/40 inline-flex cursor-pointer list-none items-center gap-1 rounded-md text-base font-semibold [&::-webkit-details-marker]:hidden">
						<ChevronRight className="size-4 transition-transform duration-150 group-open/release:rotate-90" aria-hidden="true" />
						{tu("release.fold")}
					</summary>
					<div className="mt-3 min-w-0">
						<ReleaseReportContext.Provider value={reportQ.data}>
							<PromoteEvaluation evaluationId={evaluation.id} />
						</ReleaseReportContext.Provider>
					</div>
				</details>
			)}
			{(spec.mode as string) === "model_switch" && <ModelSwitchReportView evaluationId={evaluation.id} />}
		</>
	);
	return (
		<div className={cn(pageWidthClass("dash"), "flex flex-col gap-6")} data-page-width="dash" data-testid="evaluation-screen" data-status={evaluation.status}>
			<PageHeader
				title={spec.name || evaluation.id}
				subtitle={evaluationSubtitle(t as never, evaluation, i18n.language, reportQ.data, tu as never)}
				breadcrumbs={[{ label: t("detail.crumb"), to: "/evaluations" }, { label: spec.name || evaluation.id }]}
				meta={
					<span className="flex flex-wrap items-center gap-2">
						{notStarted ? (
							<EvaluationStatusPill status="notStarted" testId="evaluation-status" />
						) : (
							<EvaluationLivePill evaluation={evaluation} testId="evaluation-status" />
						)}
						{evaluation.schedule && (
							<Badge variant="outline" data-testid="evaluation-scheduled">
								{t("detail.scheduled")}
							</Badge>
						)}
					</span>
				}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						<ShareButton kind="evaluation" target={evaluation.id} evaluationId={evaluation.id} />
						{notStarted ? (
							<Button asChild size="sm" data-testid="evaluation-run-again">
								<Link to={"/evaluate" as never} search={runAgainSearch(evaluation) as never}>
									{t("detail.runAgain")}
								</Link>
							</Button>
						) : (
							<EvaluationRunActions evaluation={evaluation} />
						)}
						{active && <CancelButton id={evaluation.id} spent={formatCost(evaluationSpend(evaluation) ?? 0)} />}
						{/* Schedules and Delete live in one More menu, schedules only on a finished evaluation (M3-11). */}
						<DropdownMenu
							label={t("detail.more")}
							testId="evaluation-more"
							items={[]}
							contentClassName="w-64 gap-2 p-2"
							content={
<EvaluationMoreItems evaluation={evaluation} name={name} />
							}
							trigger={(props) => (
								<Button {...props} variant="outline" size="sm" data-testid="evaluation-more-button">
									{t("detail.more")}
								</Button>
							)}
						/>
					</div>
				}
			/>
			<EvaluationRerunNotice evalId={evaluation.id} />
			{problem && (
				<p
					className="border-chart-error/40 bg-chart-error/10 text-chart-error-ink rounded-lg border px-4 py-2 text-sm break-words"
					role="alert"
					data-testid="evaluation-problem"
				>
					<ProblemText raw={problem} benchmark={spec.benchmarks?.[0] ?? name} />
				</p>
			)}
			{evaluation.status === "queued" && queuedReason(evaluation) && (
				<p className="text-muted-foreground text-sm" role="status" data-testid="evaluation-queued-reason">
					{t(`status.queuedReason.${queuedReason(evaluation)!.key}` as never, queuedReason(evaluation)!.opts)}
				</p>
			)}
			{starting && !problem && (
				<p className="text-muted-foreground text-sm" role="status" data-testid="evaluation-starting">
					{starting.name
						? t("detail.starting", { n: starting.n, m: starting.m, name: starting.name })
						: t("detail.startingNoName", { n: starting.n, m: starting.m })}
				</p>
			)}
			{done && (spec.mode === "versions" || spec.mode === "score") && <ReleaseSummaryLine evaluationId={evaluation.id} />}
			{/* The result of a finished evaluation comes first, before setup and progress (M3-1). */}
			{report}
			{!done && <RaiseBudgetPanel evaluation={evaluation} />}
			<section className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="evaluation-header">
				{/* Setup as a definition list (M3-17). */}
				<dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
					<div className="flex flex-col">
						<dt className="text-muted-foreground text-xs">{t("list.col.mode")}</dt>
						<dd className="font-medium" data-testid="evaluation-mode">
							<EvaluationKindLabel evaluation={evaluation} />
						</dd>
					</div>
					<div className="flex flex-col">
						<dt className="text-muted-foreground text-xs">{t("detail.faultLabel")}</dt>
						<dd>
							{faults.length > 0
								? faults.map((f) => t(`fault.${f}` as never, { defaultValue: f })).join(", ")
								: t("detail.noFaults")}
						</dd>
					</div>
					<div className="flex flex-col">
						<dt className="text-muted-foreground text-xs">{t("detail.setupLabel")}</dt>
						<dd>{setupLine(tw as never, spec.tasks_per_benchmark, spec.trials)}</dd>
					</div>
					{budget > 0 && (
						<div className="flex flex-col">
							<dt className="text-muted-foreground text-xs">{t("detail.budgetLabel")}</dt>
							<dd className="tabular-nums" data-testid="evaluation-budget">
								{formatCost(budget)}
							</dd>
						</div>
					)}
					<div className="flex flex-col">
						<dt className="text-muted-foreground text-xs">{t("list.col.created")}</dt>
						<dd className="tabular-nums">{formatCreated(evaluation.created_at, i18n.language)}</dd>
					</div>
				</dl>
				{evaluation.schedule && (
					<p className="text-muted-foreground text-sm" data-testid="evaluation-schedule-line">
						{t("detail.scheduledLine", {
							name: spec.name || evaluation.id,
							schedule: evaluation.schedule,
							date: formatCreated(evaluation.created_at, i18n.language),
						})}
					</p>
				)}
				{spec.preset === "first_look" && <FirstLookNote tasks={spec.tasks_per_benchmark || undefined} />}
				<div className="grid gap-3 sm:grid-cols-2">
					{(spec.arms ?? []).map((arm) => (
						<ArmCard key={arm.label} arm={arm} single={(spec.arms ?? []).length < 2} />
					))}
				</div>
			</section>
			<Section
				title={t("detail.progressTitle")}
				description={
					t(spec.mode === "score" ? "detail.progressDescriptionScore" : "detail.progressDescription") +
					(active ? ` ${t("detail.liveNote")}` : "")
				}
				testId="evaluation-progress"
			>
				{note === "starting" ? (
					<EmptyNote testId="evaluation-benchmarks-starting">{tw("benchmarksStarting")}</EmptyNote>
				) : note === "none" ? (
					<EmptyNote testId="evaluation-no-benchmarks">
						{t("detail.noBenchmarks")}
						{skipped.length > 0 && (
							<span className="mt-2 flex flex-col gap-1" data-testid="evaluation-no-benchmarks-reasons">
								{childReasons(skipped).map((r) => (
									<span key={r.benchmark} className="block break-words" data-testid={`evaluation-no-benchmarks-reason-${r.benchmark}`}>
										{failureCause(r.reason, r.cause) ? (
											<ProblemText raw={r.reason} benchmark={r.benchmark} code={r.cause} />
										) : (
											tf("childReason", { benchmark: r.benchmark, reason: r.reason })
										)}
									</span>
								))}
							</span>
						)}
					</EmptyNote>
				) : null}
				{run.length === 0 && <SkillScanProblems items={skipped} />}
				{run.length === 0 ? null : (
					<ul className="flex flex-col divide-y rounded-xl border">
						{run.map((child) => (
							<ChildRow key={child.benchmark} child={child} score={spec.mode === "score"} />
						))}
					</ul>
				)}
			</Section>
			{run.length > 0 && skipped.length > 0 && (
				<Section title={t("detail.skippedTitle")} description={t("detail.skippedDescription")} testId="evaluation-skipped">
					<ul className="flex flex-col gap-1 text-sm">
						{skipped.map((child) => {
							const refusal = parseSkillRefusal(child.skipped);
							return (
								<li key={child.benchmark} data-testid={`evaluation-skipped-${child.benchmark}`}>
									<span className="font-medium">{child.benchmark}</span>
									{child.skipped &&
										(failureCause(child.skipped, (child as ChildWithCause).cause) ? (
											<span className="text-muted-foreground block">
												<ProblemText raw={child.skipped} benchmark={child.benchmark} code={(child as ChildWithCause).cause} />
											</span>
										) : (
											<span className="text-muted-foreground">: {child.skipped}</span>
										))}
									{refusal && <SkillScanProblem refusal={refusal} testId={`evaluation-skill-scan-${child.benchmark}`} />}
								</li>
							);
						})}
					</ul>
				</Section>
			)}
			{!done && active && (
				<p className="text-muted-foreground text-sm" data-testid="evaluation-report-waiting">
					{t("detail.reportWaiting")}
				</p>
			)}
			{anyRunStarted && <EvaluationBifrostSection evaluationId={evaluation.id} summary={t("detail.gateway")} />}
		</div>
	);
}

/** Where the folded release block sits, for the summary line's link. */
export const RELEASE_ANCHOR = "evaluation-release";

/**
 * One line near the top (Q-61): what is live now and a link that opens the folded release block. Reads the same
 * release preview the block reads (one request); nothing while it loads or when the server has none.
 */
function ReleaseSummaryLine({ evaluationId }: { evaluationId: string }) {
	const { t: tu } = useTranslation("ufrb");
	const preview = useGetEvaluationReleasePreviewQuery(evaluationId);
	const p = preview.data;
	if (!p || preview.isError) return null;
	const live = p.live?.version ? tu("release.live", { name: versionName(p.agent, p.live.version) }) : tu("release.nothingLive", { agent: p.agent || "-" });
	const open = () => {
		const el = document.getElementById(RELEASE_ANCHOR) as HTMLDetailsElement | null;
		if (el) el.open = true;
	};
	return (
		<p className="bg-card flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border px-4 py-3 text-sm" data-testid="evaluation-release-line">
			<span>{live}</span>
			{(p.blocked?.length ?? 0) > 0 && <span className="text-chart-warning-ink">{tu("release.blocked")}</span>}
			<a href={`#${RELEASE_ANCHOR}`} onClick={open} className="text-primary font-medium hover:underline" data-testid="evaluation-release-open">
				{tu("release.open")}
			</a>
		</p>
	);
}

function ArmCard({ arm, single }: { arm: EvaluationArm; single: boolean }) {
	const { t } = useTranslation("evaluations");
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid={`evaluation-arm-${arm.label}`}>
			{!single && <span className="text-muted-foreground text-xs font-medium">{t("detail.armTitle", { label: arm.label })}</span>}
			<dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
				<dt className="text-muted-foreground">{t("detail.armAgent")}</dt>
				<dd className="font-medium">{arm.agent}</dd>
				<dt className="text-muted-foreground">{t("detail.armVersion")}</dt>
				<dd data-testid={`evaluation-arm-version-${arm.label}`}>
					{arm.version
						? arm.latest_at_launch
							? t("detail.versionLatestAtLaunch", { n: arm.version })
							: t("detail.versionN", { n: arm.version })
						: t("detail.latestVersion")}
				</dd>
				<dt className="text-muted-foreground">{t("detail.armModel")}</dt>
				<dd className="break-all">
					{!arm.model
						? t("detail.ownModel", { defaultValue: "The agent's own model" })
						: arm.provider
							? `${arm.provider}/${arm.model}`
							: arm.model}
				</dd>
			</dl>
			<span className="sr-only">{armAgentLabel(arm)}</span>
		</div>
	);
}

/** The next step for each benchmark the skill gate refused (fix wave 3, L5), when no benchmark ran. */
function SkillScanProblems({ items }: { items: readonly EvaluationChild[] }) {
	const rows = items.flatMap((c) => {
		const refusal = parseSkillRefusal(c.skipped);
		return refusal ? [{ benchmark: c.benchmark, refusal }] : [];
	});
	if (rows.length === 0) return null;
	return (
		<div className="mt-3 flex flex-col gap-3 rounded-xl border p-4" data-testid="evaluation-skill-problems">
			{rows.map((r) => (
				<SkillScanProblem key={r.benchmark} refusal={r.refusal} testId={`evaluation-skill-scan-${r.benchmark}`} />
			))}
		</div>
	);
}

/** One benchmark: status, runs done of total (from its experiment, polled while running), links. */
export function ChildRow({ child, score = false }: { child: EvaluationChild; score?: boolean }) {
	const { t } = useTranslation("evaluations");
	const active = isActiveStatus(child.status);
	const exp = useGetExperimentQuery(child.experiment_id, { pollingInterval: active ? EVALUATION_POLL_MS : 0, skipPollingIfUnfocused: true });
	const slots = exp.data?.slots;
	// N180, N216: the worker refused the runs (for example an image it could not build) and none was counted.
	const problem = exp.data?.problem && !(slots && slots.done > 0) ? exp.data.problem : undefined;
	return (
		<li className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm" data-testid={`evaluation-child-${child.benchmark}`}>
			<span className="min-w-40 font-medium">{child.benchmark}</span>
			{problem ? (
				<span
					className="text-chart-warning-ink flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1"
					role="status"
					data-testid={`evaluation-child-problem-${child.benchmark}`}
				>
					{/* Infrastructure, not the agent: warning tone and an icon, the raw text folded away (M1-26, M3-9). */}
					<ServerCrash className="size-4 shrink-0 self-center" aria-hidden="true" />
					<span className="font-medium">{t("failure.infraTitle")}</span>
					<ProblemText
						raw={problem}
						benchmark={child.benchmark}
						code={(child as ChildWithCause).cause}
						testId={`evaluation-child-problem-text-${child.benchmark}`}
					/>
					{exp.data?.run_job_id && (
						<Link
							to="/jobs/$jobId"
							params={{ jobId: exp.data.run_job_id }}
							className="text-primary text-xs font-medium hover:underline"
							data-testid={`evaluation-child-problem-log-${child.benchmark}`}
						>
							{t("detail.childProblemLog")}
						</Link>
					)}
				</span>
			) : (
				<>
					<EvaluationStatusPill status={child.status} testId={`evaluation-child-status-${child.benchmark}`} />
					<span className="text-muted-foreground tabular-nums" data-testid={`evaluation-child-runs-${child.benchmark}`}>
						{slots ? childRunsText((key, opts) => t(key, opts), slots, active) : t("detail.runsUnknown")}
					</span>
				</>
			)}
			{childStoppedText(child) ? (
				<span className="text-chart-warning-ink text-xs font-medium" data-testid={`evaluation-child-budget-stopped-${child.benchmark}`}>
					{childStoppedText(child)}{" "}
					<a href={`#${RAISE_BUDGET_ANCHOR}`} className="text-primary hover:underline">
						{t("detail.budgetStopLine")}
					</a>
				</span>
			) : childSpendLine(child) ? (
				<span className="text-muted-foreground text-xs tabular-nums" data-testid={`evaluation-child-spend-${child.benchmark}`}>
					{childSpendLine(child)}
				</span>
			) : null}
			{!(problem || (slots && slots.done === 0 && !active)) && <span className="ml-auto flex gap-3">
				<Link
					to="/experiments/$expId"
					params={{ expId: child.experiment_id }}
					className="text-primary hover:underline"
					data-testid={`evaluation-child-open-${child.benchmark}`}
				>
					{t(score ? "detail.openResults" : "detail.openComparison")}
				</Link>
				<Link
					to="/experiments/$expId"
					params={{ expId: child.experiment_id }}
					search={{ tab: "runs" } as never}
					className="text-primary hover:underline"
					data-testid={`evaluation-child-live-${child.benchmark}`}
				>
					{t("detail.watchRuns")}
				</Link>
			</span>}
		</li>
	);
}

/** Delete (admin): confirm, delete with its benchmark comparisons, back to the list. Disabled while queued or running. */
function DeleteEvaluationAction({ id, active }: { id: string; active: boolean }) {
	const { t } = useTranslation("evaluations");
	const navigate = useNavigate();
	const [remove] = useDeleteEvaluationMutation();
	return (
		<DeleteAction
			label={t("detail.delete")}
			confirmTitle={t("detail.deleteConfirmTitle")}
			confirmBody={t("detail.deleteConfirm")}
			confirmLabel={t("detail.deleteConfirmOk")}
			cancelLabel={t("detail.deleteKeep")}
			busyReason={active ? t("detail.deleteBusy") : undefined}
			adminOnly={t("detail.deleteAdminOnly")}
			errorText={(error) => t("detail.deleteError", { error })}
			onDelete={() => remove(id).unwrap()}
			onDeleted={() => void navigate({ to: "/evaluations" })}
			testId="evaluation-delete"
		/>
	);
}

function CancelButton({ id, spent }: { id: string; spent: string }) {
	const { t } = useTranslation("evaluations");
	const confirm = useConfirm();
	const [cancel, state] = useCancelEvaluationMutation();
	const [error, setError] = useState<string | null>(null);
	return (
		<span className="flex items-center gap-2">
			{error && <span className="text-chart-error-ink text-xs">{t("detail.cancelError", { error })}</span>}
			<Button
				size="sm"
				variant="outline"
				disabled={state.isLoading}
				data-testid="evaluation-cancel"
				onClick={async () => {
					const ok = await confirm(t("detail.cancelConfirm", { spent }), {
						title: t("detail.cancelConfirmTitle"),
						confirmLabel: t("detail.cancelConfirmOk"),
						cancelLabel: t("detail.cancelKeep"),
						destructive: true,
					});
					if (!ok) return;
					setError(null);
					try {
						await cancel(id).unwrap();
					} catch (e) {
						setError(getBenchErrorMessage(e));
					}
				}}
			>
				{t("detail.cancel")}
			</Button>
		</span>
	);
}