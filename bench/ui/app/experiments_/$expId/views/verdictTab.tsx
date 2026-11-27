import { VerdictCalibration } from "@/components/calibration/calibrationPanel"; // W8 judge calibration
import { ConfigDiff } from "@/components/configDiff";
import {
	TERMINAL_STATES,
	isDebugExperiment,
	suiteName,
	systemExperiment,
	type SystemExperimentKind,
} from "@/components/experiments/experiments.utils";
import { compareHref, compareSearch } from "@/components/experiments/legacyRoutes";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { DiagnosticsTable } from "@/components/verdict/diagnosticsTable";
import { Disclosure } from "@/components/verdict/disclosure";
import { GateChecklist } from "@/components/verdict/gateChecklist";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { NaiveScoreboard } from "@/components/verdict/naiveScoreboard";
import { PolicyScorecard } from "@/components/verdict/policyScorecard";
import { TaskGrid } from "@/components/verdict/taskGrid";
import { ScoreSummary } from "@/components/verdict/scoreSummary";
import "@/components/verdict/hw09I18n";
import { SetupsTable } from "@/components/verdict/setupsTable";
import { armOrder, isScoreVerdict, isSetupsVerdict } from "@/components/verdict/verdict.utils";
import { GuardList, VerdictCard } from "@/components/verdict/verdictCard";
import { LoadError } from "@/components/runs/screenStates";
import { VerdictSummary, costNumbers, thrownByArm } from "@/components/verdict/verdictSummary";
import { NoiseFloorLine } from "@/components/integrity/varianceCard";
import { cheaperArm, observedNumber } from "@/components/verdict/armCost";
import "@/components/verdict/uf4aI18n";
import { getBenchErrorMessage, useGetCatalogQuery, useGetDiagnosticsQuery, useGetGridQuery, useGetVerdictQuery } from "@/lib/store";
import { EXPERIMENT_POLL_MS, useGetExperimentQuery, useGetVerdictQuery as useVerdictForPicker } from "@/lib/store/apis/benchApi"; // the grade picker reads benchApi directly
import { useGetCostExplainQuery } from "@/lib/store/apis/migrationApi";
import { useGetSuiteQuery } from "@/lib/store/apis/suitesLightApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { suiteGone } from "./actionsTab";
import { suiteIdOf } from "@/components/shell/entityLink";
import type { Action, ExperimentStatus, MetricDescriptor } from "@/lib/types/bench";
import { Activity, GitCompare, ListChecks, Rocket, Scale, Shield, Sigma, Table2 } from "lucide-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { gradeQuery, useGradeChoice } from "./gradeChoice";
import { VerdictHeaderStrip } from "./verdictHeaderStrip";

/** What the two setups of a comparison differ in (the experiment status `arms_differ`); an older server leaves it out. */
export type ArmsDiffer = NonNullable<ExperimentStatus["arms_differ"]>;

/**
 * "Promote this version" on the Result tab: a button to the release form for two arms; a one-arm
 * run of an evaluation is promoted from the evaluation (one release covers all its benchmarks).
 * What it offers follows what differs between the two setups (N165): a model-only comparison
 * offers the model instead, an A/A check offers nothing, and an agent change without a clear
 * winner says that promoting is an exception to the release rules.
 */
export function VerdictPromote({
	expId,
	arms,
	evaluationId,
	armsDiffer,
	decision,
	modelB,
	cheaperB,
}: {
	expId: string;
	arms: readonly string[];
	evaluationId?: string;
	armsDiffer?: ArmsDiffer;
	/** The verdict's decision; "improved" means B wins. */
	decision?: string;
	/** Setup B's model, for "Use model ..." on a model-only comparison. */
	modelB?: string;
	/** True when B scores no worse and costs 2 times less or more (M4-1): offers a check of the switch, not a recommendation. */
	cheaperB?: boolean;
}) {
	const { t } = useTranslation("u14");
	const { t: tf } = useTranslation("uf4a");
	const toEvaluation = arms.length === 1 && Boolean(evaluationId);
	if (arms.length !== 2 && !toEvaluation) return null;
	if (!toEvaluation && armsDiffer === "none") return null;
	if (!toEvaluation && armsDiffer === "model") {
		// M4-3: "Use model" only when B wins; a much cheaper B at no clear difference gets "Check switching to".
		if (decision !== "improved") {
			if (!cheaperB || decision !== "inconclusive") return null;
			return (
				<div className="flex justify-end">
					<Button size="sm" variant="outline" asChild>
						<Link to="/experiments/$expId" params={{ expId }} search={{ tab: "migration" } as never} data-testid="verdict-check-switch">
							{modelB ? tf("promote.checkSwitch", { model: modelB }) : tf("promote.checkSwitchUnknown")}
						</Link>
					</Button>
				</div>
			);
		}
		return (
			<div className="flex justify-end">
				<Button size="sm" variant="outline" asChild>
					<Link
						to="/experiments/$expId"
						params={{ expId }}
						search={{ tab: "migration" } as never}
						title={t("promote.useModelHint")}
						data-testid="verdict-use-model"
					>
						{modelB ? t("promote.useModel", { model: modelB }) : t("promote.useModelUnknown")}
					</Link>
				</Button>
			</div>
		);
	}
	const exception = !toEvaluation && armsDiffer === "agent" && decision !== "improved";
	const label = (
		<>
			<Rocket className="size-4" aria-hidden="true" />
			{t("promote.verdictButton")}
		</>
	);
	return (
		<div className="flex flex-col items-end gap-1">
			<Button size="sm" asChild>
				{toEvaluation ? (
					<Link to="/evaluations/$evalId" params={{ evalId: evaluationId! }} data-testid="verdict-promote">
						{label}
					</Link>
				) : (
					<Link to="/releases" search={{ exp: expId } as never} data-testid="verdict-promote">
						{label}
					</Link>
				)}
			</Button>
			{exception && (
				<p className="text-muted-foreground max-w-md text-right text-xs" data-testid="verdict-promote-exception">
					{t("promote.exception")}
				</p>
			)}
		</div>
	);
}

/** VerdictPromote with what the setups differ in, read from the experiment status. */
/** Whether the "Use model" advice shows (p05-4): never while the comparison is still running. */
export function showPromote(partial: boolean): boolean {
	return !partial;
}

export function ExperimentVerdictPromote({
	expId,
	arms,
	evaluationId,
	decision,
	cheaperB,
}: {
	expId: string;
	arms: readonly string[];
	evaluationId?: string;
	decision: string;
	cheaperB?: boolean;
}) {
	const experiment = useGetExperimentQuery(expId);
	const status = experiment.data;
	const modelB = status?.setups?.find((s) => s.arm === arms[1])?.model || undefined;
	return (
		<VerdictPromote
			expId={expId}
			arms={arms}
			evaluationId={evaluationId}
			armsDiffer={status?.arms_differ}
			decision={decision}
			modelB={modelB}
			cheaperB={cheaperB}
		/>
	);
}

/**
 * The Verdict tab of an experiment (DESIGN.md 18, screen 1): the decision, what changed, the naive
 * count, policy, gates, diagnostics and the task grid. ?grade= names a grade source (a regrade id,
 * or reviewed); every block says which grades it reads, so a regrade is never mixed silently with
 * the sealed grid and gates.
 */
export function VerdictTab({ expId, evaluationId }: { expId: string; evaluationId?: string }) {
	const { t } = useTranslation("bench");
	const { t: tPage } = useTranslation("experimentPage");
	const { t: tPage9 } = useTranslation("hw09");
	const { t: tHx } = useTranslation("hx01");
	const { t: tf } = useTranslation("uf4a");
	const navigate = useNavigate();
	// The newest re-score by default; the header's line says so and links the original (gradeChoice.tsx).
	const { grade, latest, setGrade } = useGradeChoice(expId);
	const [, setTab] = useQueryState("tab", parseAsString);
	const status = useGetExperimentQuery(expId);
	const live = status.data ? !TERMINAL_STATES.has(status.data.state) && status.data.state !== "created" : false;
	// A debug experiment is not analysed (D13): the server answers 404 for its verdict, so nothing is asked (N247).
	const debug = Boolean(status.data && status.data.exp_id === expId && isDebugExperiment(status.data));
	// While the comparison runs, the Result tab re-reads the verdict at the status interval so the partial numbers move (N197).
	const verdict = useGetVerdictQuery(gradeQuery(expId, grade), {
		pollingInterval: live ? EXPERIMENT_POLL_MS : 0,
		skipPollingIfUnfocused: true,
		skip: debug,
	});
	// The diagnostics come from the same analysis as the verdict, so they read the same grade source.
	const diagnostics = useGetDiagnosticsQuery(gradeQuery(expId, grade), { skip: debug });
	const grid = useGetGridQuery(expId);
	// A deleted benchmark has no task pages, so the grid shows its tasks as plain text (Q-76). The
	// verdict names the benchmark with its version (tau2@1.0.0); the lookup takes the bare name.
	const suiteLookup = useGetSuiteQuery(verdict.data?.suite ? suiteIdOf(verdict.data.suite) : skipToken);
	// The average (mean) cost per run of each of two setups (M4-2): the same query the Cost tab reads, so it is shared.
	const pairArms =
		verdict.data && !isSetupsVerdict(verdict.data.verdict) && !isScoreVerdict(verdict.data.verdict) ? armOrder(verdict.data) : [];
	const costExplain = useGetCostExplainQuery(pairArms.length === 2 ? { expId } : skipToken);
	const armCost = useMemo(() => {
		const data = costExplain.data;
		if (!data) return undefined;
		// The explanation compares the experiment's own pair; only its arms get a mean.
		return {
			[data.a.arm]: data.a.cost_per_run,
			[data.b.arm]: data.b.cost_per_run,
		};
	}, [costExplain.data]);
	const catalog = useGetCatalogQuery();

	const metrics = useMemo(() => {
		const byName: Record<string, MetricDescriptor> = {};
		for (const metric of catalog.data?.metrics ?? []) byName[metric.name] = metric;
		return byName;
	}, [catalog.data]);

	// run id -> pair id, so policy evidence opens the matched pair in the comparison.
	const runPairs = useMemo(() => {
		const byRun: Record<string, string> = {};
		for (const row of grid.data?.rows ?? []) {
			for (const cell of row.cells) if (cell.run_id) byRun[cell.run_id] = cell.pair_id;
		}
		return byRun;
	}, [grid.data]);

	const openPair = useCallback((pairId: string) => void navigate({ href: compareHref(pairId, expId) }), [navigate, expId]);
	const onNext = useCallback(
		(action: Action) => {
			// Export lives in the header's More menu now (M4-15, M4-25): open it.
			if (action === "ship") {
				const more = document.querySelector<HTMLButtonElement>('[data-testid="page-header-more-button"]');
				if (more) {
					more.scrollIntoView({ block: "nearest" });
					more.click();
				}
			} else if (action === "fix_harness") void setTab("runs");
			else void navigate({ to: "/compare", search: compareSearch(undefined, expId) as never });
		},
		[navigate, setTab, expId],
	);

	if (debug) return <DebugNotScored />;
	if (verdict.isLoading) return <VerdictSkeleton tasks={status.data?.slots.total} onRetry={verdict.refetch} />;
	if (verdict.isError || !verdict.data) {
		return (
			// M4-5: the shared load error (why, Retry when it helps, the server's text under Details).
			<LoadError title={tf("verdict.loadErrorTitle")} error={verdict.error} onRetry={() => void verdict.refetch()} testId="verdict-error" />
		);
	}

	// partial: set by the server while the experiment is not finished (fix wave 3, N197); an older server leaves it out.
	const payload = verdict.data as NonNullable<typeof verdict.data> & { partial?: boolean };
	const arms = armOrder(payload);
	const partial = payload.partial === true || live;
	const names = setupNames(status.data?.setups, tPage9);
	const system = status.data ? systemExperiment({ name: status.data.name, exp_id: status.data.exp_id }) : null;
	// One setup: a score with a range, no comparison, so the A against B blocks (guards, statistics, changes) do not apply.
	const scored = isScoreVerdict(payload.verdict);
	const source = payload.grade_source;
	// A judged suite carries a calibration block; its sealed grades are a judge's, not a verifier's.
	const judged = Boolean(payload.judge_calibration);
	const tag = (testId: string, sealedOnly = false) => (
		<GradeSourceTag source={source} judged={judged} sealedOnly={sealedOnly} testId={testId} />
	);

	const diagnosticsBody = (part: "conditions" | "meters") =>
		diagnostics.isLoading ? (
			<TableSkeleton testId={`verdict-${part}-loading`} />
		) : diagnostics.isError || !diagnostics.data ? (
			<ErrorState
				message={t("verdict.diagnostics.loadError", { error: getBenchErrorMessage(diagnostics.error) })}
				onRetry={diagnostics.refetch}
				testId={`verdict-${part === "conditions" ? "diagnostics" : "meters"}-error`}
			/>
		) : (
			<DiagnosticsTable diagnostics={diagnostics.data} arms={arms} metrics={metrics} part={part} />
		);

	return (
		<div className="flex flex-col gap-6" data-testid="verdict-content" data-exp-id={payload.exp_id}>
			{payload.mode === "synthetic" && (
				<div className="flex flex-wrap items-center gap-2">
					<span
						className="bg-chart-warning/10 text-chart-warning-ink inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
						title={tPage("header.practiceHint")}
						data-testid="verdict-practice-mode"
					>
						{tPage("header.practice")}
					</span>
				</div>
			)}
			{grade && grade !== latest?.id && (
				<p
					className="border-chart-info/40 bg-chart-info/5 text-foreground flex flex-wrap items-center gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="verdict-grade-note"
				>
					{tHx("gradeNote.reading", { source: grade })}
					<Button
						variant="link"
						size="sm"
						className="h-auto p-0 text-xs"
						onClick={() => void setGrade(null)}
						dataTestId="verdict-grade-reset"
					>
						{t("verdict.source.backToOriginal")}
					</Button>
				</p>
			)}

			<VerdictTabNotes
				partial={partial ? { done: status.data?.slots.done ?? 0, total: status.data?.slots.total ?? 0 } : undefined}
				system={system?.kind}
				suite={payload.suite}
			/>

			{isScoreVerdict(payload.verdict) ? (
				<ScoreSummary
					verdict={payload.verdict}
					naive={payload.naive}
					armMetrics={diagnostics.data?.arm_metrics}
					onCompare={() => void navigate({ to: "/evaluate" })}
				/>
			) : (
				<VerdictSummary
					verdict={payload.verdict}
					arms={arms}
					names={names}
					partial={partial}
					naive={payload.naive}
					armMetrics={diagnostics.data?.arm_metrics}
					metricsStatus={diagnostics.isError ? "error" : diagnostics.isLoading ? "loading" : undefined}
					armCost={armCost}
					// Q-153: runs thrown out for cheating, under each setup's pass count.
					thrown={thrownByArm(grid.data?.rows)}
					onNext={onNext}
				/>
			)}
			{!partial && isSetupsVerdict(payload.verdict) && (
				<SetupsTable
					setups={payload.verdict.setups}
					arms={arms}
					naive={payload.naive}
					names={names}
					costs={Object.fromEntries(arms.map((arm) => [arm, observedNumber(diagnostics.data?.arm_metrics?.[arm]?.cost_usd)]))}
				/>
			)}
			{/* Q-77, V4-7: the promote button and the Details strip sit under the verdict, never above it. */}
			<div className="flex flex-wrap items-start justify-between gap-3" data-testid="verdict-footer">
				<details className="group text-xs" data-testid="verdict-details">
					<summary
						className="text-muted-foreground hover:text-foreground cursor-pointer list-none select-none"
						title={tPage("header.detailsHint")}
					>
						{tPage("header.details")}
					</summary>
					<div className="mt-2">
						<VerdictHeaderStrip payload={payload} />
					</div>
				</details>
				{showPromote(partial) && (
					<ExperimentVerdictPromote
						expId={payload.exp_id}
						arms={arms}
						evaluationId={evaluationId}
						decision={payload.verdict.decision}
						cheaperB={cheaperArm(arms, costNumbers(arms, armCost, diagnostics.data?.arm_metrics))?.arm === arms[1]}
					/>
				)}
			</div>

			<div className="flex flex-col gap-3">
				{/* M4-8: one line says which grades every section below reads, in place of a pill on each row. */}
				<p className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs" data-testid="verdict-sections-source">
					{tf("verdict.sectionsRead")} {tag("verdict-gates-source")}
				</p>
				{!scored && (
					<Disclosure
						title={tPage("sections.guards")}
						description={tPage(arms.length > 2 ? "sectionHints.guardsMany" : "sectionHints.guards")}
						icon={Shield}
						meta={guardMeta(payload.verdict.guards)}
						testId="verdict-guards-section"
					>
						<GuardList guards={payload.verdict.guards} />
					</Disclosure>
				)}

				<Disclosure title={tPage("sections.gates")} description={tPage("sectionHints.gates")} icon={ListChecks} testId="verdict-gates">
					<GateChecklist gates={payload.gates} />
				</Disclosure>

				<Disclosure
					title={tPage("sections.diagnostics")}
					icon={Activity}
					testId="verdict-diagnostics"
				>
					{diagnosticsBody("conditions")}
				</Disclosure>

				{!scored && (
					<Disclosure
						title={tPage(arms.length > 2 ? "sections.changesMany" : "sections.changes")}
						icon={GitCompare}
						testId="verdict-config-diff"
					>
						<div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
							<ConfigDiff changes={payload.config_diff} armLabels={arms.length === 2 ? { a: arms[0], b: arms[1] } : undefined} />
							<div className="flex flex-col gap-2">
								<div className="flex justify-end">{tag("verdict-naive-source")}</div>
								<NaiveScoreboard naive={payload.naive} arms={arms} />
							</div>
						</div>
					</Disclosure>
				)}

				<Disclosure title={tPage("sections.policy")} icon={Scale} testId="verdict-policy">
					<PolicyScorecard policy={payload.policy} expId={payload.exp_id} metrics={metrics} runPairs={runPairs} onOpenPair={openPair} />
				</Disclosure>

				{/* W8 judge calibration: nothing for a verified suite */}
				{payload.judge_calibration && <VerdictCalibration jc={payload.judge_calibration} />}

				{/* The grid shows each run's sealed outcome; a regrade does not change it, and the tag says so. */}
				<Disclosure title={tPage("sections.tasks")} description={tPage("sectionHints.tasks")} icon={Table2} testId="verdict-grid">
					{grid.isLoading ? (
						<TableSkeleton testId="verdict-grid-loading" />
					) : grid.isError || !grid.data ? (
						<ErrorState
							message={t("verdict.grid.loadError", { error: getBenchErrorMessage(grid.error) })}
							onRetry={grid.refetch}
							testId="verdict-grid-error"
						/>
					) : (
						<TaskGrid grid={grid.data} onOpenPair={openPair} suite={payload.suite} suiteGone={suiteGone(suiteLookup.error)} />
					)}
				</Disclosure>

				{/* M4-8, M4-16: Measurements and Advanced statistics in one section at the end, for statisticians. */}
				<Disclosure
					title={tf("verdict.forStatisticians")}
					icon={Sigma}
					testId="verdict-statistics"
				>
					<div className="flex flex-col gap-6">
						<div className="flex flex-col gap-2" data-testid="verdict-meters">
							<h4 className="text-sm font-medium">{tPage("sections.meters")}</h4>
							{diagnosticsBody("meters")}
						</div>
						{!scored && (
							<div className="flex flex-col gap-2">
								<h4 className="text-sm font-medium">{tPage("sections.statistics")}</h4>
								<VerdictCard verdict={payload.verdict} metricLabel={metrics[payload.verdict.primary.metric]?.label} showGuards={false} />
							</div>
						)}
					</div>
				</Disclosure>
			</div>
		</div>
	);
}

const SEALED = "original";

type THw09 = ReturnType<typeof useTranslation<"hw09">>["t"];

/** arm -> "codex on gpt-6.2-sol", from the experiment status; undefined until it loads. */
function setupNames(setups: ExperimentStatus["setups"], t: THw09): Record<string, string> | undefined {
	if (!setups?.length) return undefined;
	const out: Record<string, string> = {};
	for (const s of setups) {
		if (s.agent && s.model) out[s.arm] = t("setups.name", { agent: s.agent, model: s.model });
		else if (s.agent || s.model) out[s.arm] = s.agent || s.model;
	}
	return out;
}

/**
 * The notes above the result (fix wave 3): while the comparison runs, how many runs are done and
 * that the result is not final (N197); for a check Heimdall ran itself, that it is not a
 * comparison, with a link to the benchmark's Build tab for a readiness check (N253).
 */
/** The Result tab of a debug experiment: its runs are not scored, so there is no verdict and no error (N247). */
export function DebugNotScored() {
	const { t } = useTranslation("experimentsUx");
	return (
		<p className="text-muted-foreground rounded-sm border px-4 py-3 text-sm" data-testid="verdict-debug-not-scored">
			{t("kind.debug")}
		</p>
	);
}

export function VerdictTabNotes({
	partial,
	system,
	suite,
}: {
	partial?: { done: number; total: number };
	system?: SystemExperimentKind;
	suite?: string;
}) {
	const { t } = useTranslation("hw09");
	const suiteId = suiteName(suite);
	return (
		<>
			{partial && (
				<p
					role="status"
					className="border-chart-info/40 bg-chart-info/5 text-foreground rounded-sm border px-3 py-2 text-sm"
					data-testid="verdict-partial-note"
				>
					{t("result.partial", { done: partial.done, total: partial.total })}
				</p>
			)}
			{system && (
				<p
					className="border-border bg-muted/50 text-foreground flex flex-wrap items-center gap-2 rounded-sm border px-3 py-2 text-sm"
					data-testid="verdict-system-note"
				>
					{t(`result.system.${system}`)}
					{system === "readiness" && suiteId && suiteId.startsWith("draft-") && (
						<Link to="/suites/add" className="text-primary hover:underline" data-testid="verdict-system-link">
							{t("result.system.open")}
						</Link>
					)}
					{system === "readiness" && suiteId && !suiteId.startsWith("draft-") && (
						<Link
							to="/suites/$suiteId"
							params={{ suiteId }}
							search={{ tab: "build" } as never}
							className="text-primary hover:underline"
							data-testid="verdict-system-link"
						>
							{t("result.system.open")}
						</Link>
					)}
				</p>
			)}
		</>
	);
}

/** Guard counts for the closed Guards row: "2 pass, 1 fail". */
function guardMeta(guards: { status: string }[]): string | undefined {
	if (guards.length === 0) return undefined;
	const counts = new Map<string, number>();
	for (const guard of guards) counts.set(guard.status, (counts.get(guard.status) ?? 0) + 1);
	return [...counts].map(([status, n]) => `${n} ${status}`).join(", ");
}

/**
 * The "Which grades" select of the experiment page (M4-14): it sits in the Run controls, not in the
 * verdict's top row. Reads the same grade choice and verdict as the Result tab (one cached request).
 */
export function ExperimentGradePicker({ expId }: { expId: string }) {
	const { grade, setGrade } = useGradeChoice(expId);
	const status = useGetExperimentQuery(expId);
	const debug = Boolean(status.data && status.data.exp_id === expId && isDebugExperiment(status.data));
	const verdict = useVerdictForPicker(gradeQuery(expId, grade), { skip: debug || !status.data });
	const judged = Boolean(verdict.data?.judge_calibration);
	return <GradeSourcePicker value={grade} judged={judged} onChange={(next) => void setGrade(next)} hideLabel />;
}

/** Which grades the verdict reads: the sealed ones, human reviews, or the regrade named in the URL. */
function GradeSourcePicker({
	value,
	judged,
	onChange,
	hideLabel,
}: {
	value: string | null;
	judged: boolean;
	onChange: (next: string | null) => void;
	hideLabel?: boolean;
}) {
	const { t } = useTranslation("bench");
	const current = value ?? SEALED;
	return (
		<label className="flex items-center gap-2 text-xs">
			<span className={hideLabel ? "sr-only" : "text-muted-foreground"}>{t("verdict.source.pickLabel")}</span>
			<Select value={current} onValueChange={(next) => onChange(next === SEALED ? null : next)}>
				<SelectTrigger size="sm" className="w-44 text-xs" data-testid="verdict-grade-source-picker">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={SEALED}>{t(judged ? "verdict.source.pickJudged" : "verdict.source.pickVerified")}</SelectItem>
					<SelectItem value="reviewed">{t("verdict.source.pickReviewed")}</SelectItem>
					{current !== SEALED && current !== "reviewed" && (
						<SelectItem value={current}>{t("verdict.source.regradedId", { id: current })}</SelectItem>
					)}
				</SelectContent>
			</Select>
		</label>
	);
}

export function Section({
	title,
	description,
	testId,
	action,
	children,
}: {
	title: string;
	description?: string;
	testId: string;
	action?: ReactNode;
	children: ReactNode;
}) {
	return (
		<Card className="gap-4" data-testid={testId}>
			<CardHeader>
				<CardTitle role="heading" aria-level={2}>
					{title}
				</CardTitle>
				{description && <CardDescription>{description}</CardDescription>}
				{action && <CardAction>{action}</CardAction>}
			</CardHeader>
			<CardContent className="overflow-x-auto">{children}</CardContent>
		</Card>
	);
}

export function ErrorState({ message, onRetry, testId }: { message: string; onRetry: () => void; testId: string }) {
	const { t } = useTranslation("bench");
	return (
		<div
			role="alert"
			className="border-chart-error/50 bg-chart-error/5 text-chart-error-ink flex flex-wrap items-center justify-between gap-3 rounded-sm border px-4 py-3 text-sm"
			data-testid={testId}
		>
			<span>{message}</span>
			<Button variant="outline" size="sm" onClick={onRetry} dataTestId={`${testId}-retry`}>
				{t("verdict.retry")}
			</Button>
		</div>
	);
}

function TableSkeleton({ testId }: { testId: string }) {
	return (
		<div className="flex flex-col gap-2" data-testid={testId}>
			<Skeleton className="h-8 w-full" />
			<Skeleton className="h-8 w-full" />
			<Skeleton className="h-8 w-full" />
			<Skeleton className="h-8 w-2/3" />
		</div>
	);
}

/** Which stage a slow verdict load is in (M4-5): quiet, then a still-working note at 8 s, then an error at 30 s. */
export function slowStage(elapsedMs: number): "wait" | "slow" | "stuck" {
	return elapsedMs >= 30_000 ? "stuck" : elapsedMs >= 8_000 ? "slow" : "wait";
}

function VerdictSkeleton({ tasks, onRetry }: { tasks?: number; onRetry: () => void }) {
	const { t } = useTranslation("uf4a");
	const [elapsed, setElapsed] = useState(0);
	useEffect(() => {
		const start = Date.now();
		const timer = window.setInterval(() => setElapsed(Date.now() - start), 1000);
		return () => window.clearInterval(timer);
	}, []);
	const stage = slowStage(elapsed);
	if (stage === "stuck") {
		return <ErrorState message={t("verdict.stuck")} onRetry={onRetry} testId="verdict-stuck" />;
	}
	return (
		<div className="flex flex-col gap-6" data-testid="verdict-loading">
			{stage === "slow" && (
				<div className="flex flex-wrap items-center gap-3 text-sm" role="status" data-testid="verdict-slow">
					<span>{tasks ? t("verdict.slowCount", { count: tasks }) : t("verdict.slow")}</span>
					<Button variant="outline" size="sm" onClick={onRetry} dataTestId="verdict-slow-retry">
						{t("verdict.retry")}
					</Button>
				</div>
			)}
			<Skeleton className="h-6 w-2/3" />
			<Skeleton className="h-64 w-full" />
			<div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
				<Skeleton className="h-40 w-full" />
				<Skeleton className="h-40 w-full" />
			</div>
			<Skeleton className="h-64 w-full" />
		</div>
	);
}