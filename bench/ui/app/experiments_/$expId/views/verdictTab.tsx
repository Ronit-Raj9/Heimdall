import { VerdictCalibration } from "@/components/calibration/calibrationPanel"; // W8 judge calibration
import { ConfigDiff } from "@/components/configDiff";
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
import { armOrder, isScoreVerdict } from "@/components/verdict/verdict.utils";
import { GuardList, VerdictCard } from "@/components/verdict/verdictCard";
import { VerdictSummary } from "@/components/verdict/verdictSummary";
import { getBenchErrorMessage, useGetCatalogQuery, useGetDiagnosticsQuery, useGetGridQuery, useGetVerdictQuery } from "@/lib/store";
import { useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import type { Action, ExperimentStatus, MetricDescriptor } from "@/lib/types/bench";
import { Activity, Gauge, GitCompare, ListChecks, Rocket, Scale, Shield, Sigma, Table2 } from "lucide-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
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
}: {
	expId: string;
	arms: readonly string[];
	evaluationId?: string;
	armsDiffer?: ArmsDiffer;
	/** The verdict's decision; "improved" means B wins. */
	decision?: string;
	/** Setup B's model, for "Use model ..." on a model-only comparison. */
	modelB?: string;
}) {
	const { t } = useTranslation("u14");
	const toEvaluation = arms.length === 1 && Boolean(evaluationId);
	if (arms.length !== 2 && !toEvaluation) return null;
	if (!toEvaluation && armsDiffer === "none") return null;
	if (!toEvaluation && armsDiffer === "model") {
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
export function ExperimentVerdictPromote({
	expId,
	arms,
	evaluationId,
	decision,
}: {
	expId: string;
	arms: readonly string[];
	evaluationId?: string;
	decision: string;
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
	const navigate = useNavigate();
	const [grade, setGrade] = useQueryState("grade", parseAsString);
	const [, setTab] = useQueryState("tab", parseAsString);
	const verdict = useGetVerdictQuery(grade ? { expId, gradeSource: grade } : expId);
	// The diagnostics come from the same analysis as the verdict, so they read the same grade source.
	const diagnostics = useGetDiagnosticsQuery(grade ? { expId, gradeSource: grade } : expId);
	const grid = useGetGridQuery(expId);
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
			if (action === "ship") void setTab("export");
			else if (action === "fix_harness") void setTab("runs");
			else void navigate({ to: "/compare", search: compareSearch(undefined, expId) as never });
		},
		[navigate, setTab, expId],
	);

	if (verdict.isLoading) return <VerdictSkeleton />;
	if (verdict.isError || !verdict.data) {
		return (
			<ErrorState
				message={t("verdict.loadError", { id: expId, error: getBenchErrorMessage(verdict.error) })}
				onRetry={verdict.refetch}
				testId="verdict-error"
			/>
		);
	}

	const payload = verdict.data;
	const arms = armOrder(payload);
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
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex flex-wrap items-center gap-2">
					{payload.mode === "synthetic" && (
						<span
							className="bg-chart-warning/10 text-chart-warning-ink inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
							title={tPage("header.practiceHint")}
							data-testid="verdict-practice-mode"
						>
							{tPage("header.practice")}
						</span>
					)}
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
				</div>
				<GradeSourcePicker value={grade} judged={judged} onChange={(next) => void setGrade(next)} />
			</div>
			{grade && (
				<p
					className="border-chart-info/40 bg-chart-info/5 text-foreground flex flex-wrap items-center gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="verdict-grade-note"
				>
					{t("verdict.source.reading", { source: grade })}
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

			<ExperimentVerdictPromote expId={payload.exp_id} arms={arms} evaluationId={evaluationId} decision={payload.verdict.decision} />

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
					naive={payload.naive}
					armMetrics={diagnostics.data?.arm_metrics}
					metricsStatus={diagnostics.isError ? "error" : diagnostics.isLoading ? "loading" : undefined}
					onNext={onNext}
				/>
			)}

			<div className="flex flex-col gap-3">
				{!scored && (
					<Disclosure
						title={tPage("sections.guards")}
						description={tPage("sectionHints.guards")}
						icon={Shield}
						meta={guardMeta(payload.verdict.guards)}
						testId="verdict-guards-section"
					>
						<GuardList guards={payload.verdict.guards} />
					</Disclosure>
				)}

				<Disclosure
					title={tPage("sections.gates")}
					description={tPage("sectionHints.gates")}
					icon={ListChecks}
					action={tag("verdict-gates-source", true)}
					testId="verdict-gates"
				>
					<GateChecklist gates={payload.gates} />
				</Disclosure>

				<Disclosure
					title={tPage("sections.diagnostics")}
					description={tPage("sectionHints.diagnostics")}
					icon={Activity}
					action={tag("verdict-diagnostics-source")}
					testId="verdict-diagnostics"
				>
					{diagnosticsBody("conditions")}
				</Disclosure>

				<Disclosure title={tPage("sections.meters")} description={tPage("sectionHints.meters")} icon={Gauge} testId="verdict-meters">
					{diagnosticsBody("meters")}
				</Disclosure>

				{!scored && (
					<Disclosure
						title={tPage("sections.statistics")}
						description={tPage("sectionHints.statistics")}
						icon={Sigma}
						action={tag("verdict-card-source")}
						testId="verdict-statistics"
					>
						<VerdictCard verdict={payload.verdict} metricLabel={metrics[payload.verdict.primary.metric]?.label} showGuards={false} />
					</Disclosure>
				)}

				{!scored && (
					<Disclosure
						title={tPage("sections.changes")}
						description={tPage("sectionHints.changes")}
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

				<Disclosure
					title={tPage("sections.policy")}
					description={tPage("sectionHints.policy")}
					icon={Scale}
					action={tag("verdict-policy-source")}
					testId="verdict-policy"
				>
					<PolicyScorecard policy={payload.policy} expId={payload.exp_id} metrics={metrics} runPairs={runPairs} onOpenPair={openPair} />
				</Disclosure>

				{/* W8 judge calibration: nothing for a verified suite */}
				{payload.judge_calibration && <VerdictCalibration jc={payload.judge_calibration} />}

				{/* The grid shows each run's sealed outcome; a regrade does not change it, and the tag says so. */}
				<Disclosure
					title={tPage("sections.tasks")}
					description={tPage("sectionHints.tasks")}
					icon={Table2}
					action={tag("verdict-grid-source", true)}
					testId="verdict-grid"
				>
					{grid.isLoading ? (
						<TableSkeleton testId="verdict-grid-loading" />
					) : grid.isError || !grid.data ? (
						<ErrorState
							message={t("verdict.grid.loadError", { error: getBenchErrorMessage(grid.error) })}
							onRetry={grid.refetch}
							testId="verdict-grid-error"
						/>
					) : (
						<TaskGrid grid={grid.data} onOpenPair={openPair} suite={payload.suite} />
					)}
				</Disclosure>
			</div>
		</div>
	);
}

const SEALED = "original";

/** Guard counts for the closed Guards row: "2 pass, 1 fail". */
function guardMeta(guards: { status: string }[]): string | undefined {
	if (guards.length === 0) return undefined;
	const counts = new Map<string, number>();
	for (const guard of guards) counts.set(guard.status, (counts.get(guard.status) ?? 0) + 1);
	return [...counts].map(([status, n]) => `${n} ${status}`).join(", ");
}

/** Which grades the verdict reads: the sealed ones, human reviews, or the regrade named in the URL. */
function GradeSourcePicker({
	value,
	judged,
	onChange,
}: {
	value: string | null;
	judged: boolean;
	onChange: (next: string | null) => void;
}) {
	const { t } = useTranslation("bench");
	const current = value ?? SEALED;
	return (
		<label className="flex items-center gap-2 text-xs">
			<span className="text-muted-foreground">{t("verdict.source.pickLabel")}</span>
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
				<CardTitle>{title}</CardTitle>
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

function VerdictSkeleton() {
	return (
		<div className="flex flex-col gap-6" data-testid="verdict-loading">
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