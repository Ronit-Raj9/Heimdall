import { ConfigDiff } from "@/components/configDiff";
import { ExperimentGatewayPolicy } from "@/components/routing/gatewayPolicyCard"; // W9 routing policy under test
import { ExperimentSupportScorecard } from "@/components/support/supportScorecard"; // W3 customer support
import { ExperimentResearchScorecard } from "@/components/research/researchScorecard"; // W4 research with Tavily
import { ExperimentTrialsPanel } from "@/components/adaptive/trialsPanel"; // W7 adaptive trials and suite tiers
import { ExperimentExportMenu } from "@/components/export/exportMenu"; // W10 export
import { VerdictCalibration } from "@/components/calibration/calibrationPanel"; // W8 judge calibration
import { CostExplainPanel } from "@/components/migration/costWaterfall"; // W6 cost explanation
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { DiagnosticsTable } from "@/components/verdict/diagnosticsTable";
import { GateChecklist } from "@/components/verdict/gateChecklist";
import { NaiveScoreboard } from "@/components/verdict/naiveScoreboard";
import { PolicyScorecard } from "@/components/verdict/policyScorecard";
import { TaskGrid } from "@/components/verdict/taskGrid";
import { VerdictCard } from "@/components/verdict/verdictCard";
import { armOrder, sortExperimentsNewestFirst } from "@/components/verdict/verdict.utils";
import {
	getBenchErrorMessage,
	useGetCatalogQuery,
	useGetDiagnosticsQuery,
	useGetGridQuery,
	useGetVerdictQuery,
	useListExperimentsQuery,
} from "@/lib/store";
import type { MetricDescriptor } from "@/lib/types/bench";
import { Link, useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback, useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ExperimentPicker } from "./experimentPicker";
import { VerdictHeaderStrip } from "./verdictHeaderStrip";

/** The verdict screen (DESIGN.md 18, screen 1). The experiment id lives in the URL as ?exp=. */
export default function VerdictView() {
	const { t } = useTranslation("bench");
	const [exp, setExp] = useQueryState("exp", parseAsString);
	const list = useListExperimentsQuery();
	const experiments = useMemo(() => sortExperimentsNewestFirst(list.data?.experiments ?? []), [list.data]);
	// Default to the newest experiment without writing it to the URL, so "newest" stays a live default.
	const selectedId = exp ?? experiments[0]?.exp_id;

	return (
		<section className="flex flex-col gap-6" data-testid="verdict-screen">
			<div className="flex flex-wrap items-end justify-between gap-4">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("verdict.title")}</h1>
				{list.isLoading ? (
					<Skeleton className="h-9 w-72" data-testid="verdict-experiment-picker-loading" />
				) : (
					<ExperimentPicker
						experiments={experiments}
						value={selectedId}
						onChange={(id) => void setExp(id)}
						disabled={experiments.length === 0}
					/>
				)}
			</div>
			{list.isError && (
				<ErrorState
					message={t("verdict.picker.loadError", { error: getBenchErrorMessage(list.error) })}
					onRetry={list.refetch}
					testId="verdict-picker-error"
				/>
			)}
			{selectedId ? <ExperimentVerdict expId={selectedId} /> : list.isLoading ? <VerdictSkeleton /> : !list.isError && <NoExperiments />}
		</section>
	);
}

function ExperimentVerdict({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const navigate = useNavigate();
	// ?grade= names a grade source (a regrade id, or reviewed for human reviews); absent is the sealed original.
	const [grade] = useQueryState("grade", parseAsString);
	const verdict = useGetVerdictQuery(grade ? { expId, gradeSource: grade } : expId);
	const diagnostics = useGetDiagnosticsQuery(expId);
	const grid = useGetGridQuery(expId);
	const catalog = useGetCatalogQuery();

	const metrics = useMemo(() => {
		const byName: Record<string, MetricDescriptor> = {};
		for (const metric of catalog.data?.metrics ?? []) byName[metric.name] = metric;
		return byName;
	}, [catalog.data]);

	// run id -> pair id, so policy evidence opens the matched pair in the run inspector.
	const runPairs = useMemo(() => {
		const byRun: Record<string, string> = {};
		for (const row of grid.data?.rows ?? []) {
			for (const cell of row.cells) if (cell.run_id) byRun[cell.run_id] = cell.pair_id;
		}
		return byRun;
	}, [grid.data]);

	const openPair = useCallback(
		(pairId: string) => {
			void navigate({ href: `/runs?pair=${encodeURIComponent(pairId)}&exp=${encodeURIComponent(expId)}` });
		},
		[navigate, expId],
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

	return (
		<div className="flex flex-col gap-6" data-testid="verdict-content" data-exp-id={payload.exp_id}>
			<VerdictHeaderStrip payload={payload} />

			{/* The decision opens the page; what changed and the naive count sit beside each other under it. */}
			<VerdictCard verdict={payload.verdict} metricLabel={metrics[payload.verdict.primary.metric]?.label} />

			<div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
				<Section title={t("verdict.configDiff.title")} description={t("verdict.configDiff.description")} testId="verdict-config-diff">
					<ConfigDiff changes={payload.config_diff} armLabels={arms.length === 2 ? { a: arms[0], b: arms[1] } : undefined} />
				</Section>
				<NaiveScoreboard naive={payload.naive} arms={arms} />
			</div>

			<Section title={t("policy.title")} description={t("policy.description")} testId="verdict-policy">
				<PolicyScorecard policy={payload.policy} expId={payload.exp_id} metrics={metrics} runPairs={runPairs} onOpenPair={openPair} />
			</Section>

			<Section title={t("verdict.gates.title")} description={t("verdict.gates.description")} testId="verdict-gates">
				<GateChecklist gates={payload.gates} />
			</Section>

			{/* W9 routing policy under test: nothing for fixed-key experiments */}
			<ExperimentGatewayPolicy expId={payload.exp_id} />

			{/* W3 customer support: resolution and escalation cards; nothing for a non-support suite */}
			<ExperimentSupportScorecard expId={payload.exp_id} />
			{/* W4 research with Tavily: nothing for experiments whose runs made no Tavily searches */}
			<ExperimentResearchScorecard expId={payload.exp_id} />
			{/* W7 adaptive trials and suite tiers: nothing for a fixed experiment with no tier */}
			<ExperimentTrialsPanel expId={payload.exp_id} />
			{/* W6 cost explanation: why arm B cost more than arm A */}
			<CostExplainPanel expId={payload.exp_id} />
			{/* W10 export: EEE records and the run table as downloads */}
			<ExperimentExportMenu expId={payload.exp_id} />
			{/* W8 judge calibration: nothing for a verified suite */}
			{payload.judge_calibration && <VerdictCalibration jc={payload.judge_calibration} />}

			<Section title={t("verdict.diagnostics.title")} description={t("verdict.diagnostics.description")} testId="verdict-diagnostics">
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

			<Section title={t("verdict.grid.title")} description={t("verdict.grid.description")} testId="verdict-grid">
				{grid.isLoading ? (
					<TableSkeleton testId="verdict-grid-loading" />
				) : grid.isError || !grid.data ? (
					<ErrorState
						message={t("verdict.grid.loadError", { error: getBenchErrorMessage(grid.error) })}
						onRetry={grid.refetch}
						testId="verdict-grid-error"
					/>
				) : (
					<TaskGrid grid={grid.data} onOpenPair={openPair} />
				)}
			</Section>
		</div>
	);
}

function Section({ title, description, testId, children }: { title: string; description?: string; testId: string; children: ReactNode }) {
	return (
		<Card className="gap-4" data-testid={testId}>
			<CardHeader>
				<CardTitle>{title}</CardTitle>
				{description && <CardDescription>{description}</CardDescription>}
			</CardHeader>
			<CardContent className="overflow-x-auto">{children}</CardContent>
		</Card>
	);
}

function ErrorState({ message, onRetry, testId }: { message: string; onRetry: () => void; testId: string }) {
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

function NoExperiments() {
	const { t } = useTranslation("bench");
	return (
		<Card className="border-dashed py-10 shadow-none" data-testid="verdict-empty-state">
			<CardContent className="flex flex-col items-start gap-3">
				<p className="text-muted-foreground max-w-prose text-sm">{t("verdict.noExperiments")}</p>
				<Button variant="outline" size="sm" asChild>
					<Link to="/experiments/new" data-testid="verdict-empty-new-experiment">
						{t("verdict.newExperiment")}
					</Link>
				</Button>
			</CardContent>
		</Card>
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