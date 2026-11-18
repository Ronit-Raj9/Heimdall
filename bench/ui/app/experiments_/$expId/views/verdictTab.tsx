import { VerdictCalibration } from "@/components/calibration/calibrationPanel"; // W8 judge calibration
import { ConfigDiff } from "@/components/configDiff";
import { compareHref } from "@/components/experiments/legacyRoutes";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { DiagnosticsTable } from "@/components/verdict/diagnosticsTable";
import { GateChecklist } from "@/components/verdict/gateChecklist";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { NaiveScoreboard } from "@/components/verdict/naiveScoreboard";
import { PolicyScorecard } from "@/components/verdict/policyScorecard";
import { TaskGrid } from "@/components/verdict/taskGrid";
import { armOrder } from "@/components/verdict/verdict.utils";
import { VerdictCard } from "@/components/verdict/verdictCard";
import { getBenchErrorMessage, useGetCatalogQuery, useGetDiagnosticsQuery, useGetGridQuery, useGetVerdictQuery } from "@/lib/store";
import type { MetricDescriptor } from "@/lib/types/bench";
import { useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { VerdictHeaderStrip } from "./verdictHeaderStrip";

/**
 * The Verdict tab of an experiment (DESIGN.md 18, screen 1): the decision, what changed, the naive
 * count, policy, gates, diagnostics and the task grid. ?grade= names a grade source (a regrade id,
 * or reviewed); every block says which grades it reads, so a regrade is never mixed silently with
 * the sealed grid and gates.
 */
export function VerdictTab({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const navigate = useNavigate();
	const [grade, setGrade] = useQueryState("grade", parseAsString);
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
	const source = payload.grade_source;
	// A judged suite carries a calibration block; its sealed grades are a judge's, not a verifier's.
	const judged = Boolean(payload.judge_calibration);
	const tag = (testId: string, sealedOnly = false) => (
		<GradeSourceTag source={source} judged={judged} sealedOnly={sealedOnly} testId={testId} />
	);

	return (
		<div className="flex flex-col gap-6" data-testid="verdict-content" data-exp-id={payload.exp_id}>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<VerdictHeaderStrip payload={payload} />
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

			<div className="flex flex-col gap-2">
				<div className="flex justify-end">{tag("verdict-card-source")}</div>
				<VerdictCard verdict={payload.verdict} metricLabel={metrics[payload.verdict.primary.metric]?.label} />
			</div>

			<div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
				<Section title={t("verdict.configDiff.title")} description={t("verdict.configDiff.description")} testId="verdict-config-diff">
					<ConfigDiff changes={payload.config_diff} armLabels={arms.length === 2 ? { a: arms[0], b: arms[1] } : undefined} />
				</Section>
				<div className="flex flex-col gap-2">
					<div className="flex justify-end">{tag("verdict-naive-source")}</div>
					<NaiveScoreboard naive={payload.naive} arms={arms} />
				</div>
			</div>

			<Section
				title={t("policy.title")}
				description={t("policy.description")}
				testId="verdict-policy"
				action={tag("verdict-policy-source")}
			>
				<PolicyScorecard policy={payload.policy} expId={payload.exp_id} metrics={metrics} runPairs={runPairs} onOpenPair={openPair} />
			</Section>

			<Section
				title={t("verdict.gates.title")}
				description={t("verdict.gates.description")}
				testId="verdict-gates"
				action={tag("verdict-gates-source", true)}
			>
				<GateChecklist gates={payload.gates} />
			</Section>

			{/* W8 judge calibration: nothing for a verified suite */}
			{payload.judge_calibration && <VerdictCalibration jc={payload.judge_calibration} />}

			<Section
				title={t("verdict.diagnostics.title")}
				description={t("verdict.diagnostics.description")}
				testId="verdict-diagnostics"
				action={tag("verdict-diagnostics-source")}
			>
				{diagnostics.isLoading ? (
					<TableSkeleton testId="verdict-diagnostics-loading" />
				) : diagnostics.isError || !diagnostics.data ? (
					<ErrorState
						message={t("verdict.diagnostics.loadError", { error: getBenchErrorMessage(diagnostics.error) })}
						onRetry={diagnostics.refetch}
						testId="verdict-diagnostics-error"
					/>
				) : (
					<DiagnosticsTable diagnostics={diagnostics.data} arms={arms} metrics={metrics} />
				)}
			</Section>

			{/* The grid shows each run's sealed outcome; a regrade does not change it, and the tag says so. */}
			<Section
				title={t("verdict.grid.title")}
				description={t("verdict.grid.description")}
				testId="verdict-grid"
				action={tag("verdict-grid-source", true)}
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
			</Section>
		</div>
	);
}

const SEALED = "original";

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