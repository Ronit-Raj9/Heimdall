import { Section } from "@/components/ui/section";
import { useGetClustersQuery } from "@/lib/store/apis/benchApi";
import {
	patternRate,
	seenPatterns,
	useGetEvaluationFailurePatternsQuery,
	type FailureExample,
	type FailurePatternGroup,
} from "@/lib/store/apis/failurePatternsApi";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/agentFirstI18n";
import type { ClusterSummary } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { pct } from "./reportCard.utils";
import "./reportCardI18n";

/** Failure groups listed on the report card. */
export const REPORT_CLUSTERS_SHOWN = 3;

/** The child experiment ids a report card was built from, once each. */
export function reportExperiments(report: Pick<Report, "benchmarks">): string[] {
	return [...new Set((report.benchmarks ?? []).map((b) => b.experiment_id).filter(Boolean))];
}

/** The groups for the arms the report shows (all when it names none). */
export function groupsForReport(groups: readonly FailurePatternGroup[], report: Pick<Report, "arms">): FailurePatternGroup[] {
	const labels = new Set((report.arms ?? []).map((a) => a.label));
	return labels.size === 0 ? [...groups] : groups.filter((g) => labels.has(g.key));
}

/** One example run as a link to its page. */
export function ExampleRunLink({ example }: { example: FailureExample }) {
	return (
		<Link
			to="/runs/$runId"
			params={{ runId: example.run_id }}
			className="text-primary inline-flex max-w-full items-center gap-1 truncate font-mono text-xs underline-offset-4 hover:underline"
			title={example.run_id}
			data-testid={`failure-example-${example.run_id}`}
		>
			{example.benchmark && example.task ? `${example.benchmark}/${example.task}` : example.run_id}
			<ArrowUpRight className="size-3 shrink-0" aria-hidden="true" />
		</Link>
	);
}

/** One version's or arm's patterns: how often each showed up, and example runs. */
export function PatternList({ group, testId }: { group: FailurePatternGroup; testId?: string }) {
	const { t } = useTranslation("agentFirst");
	const { t: tr } = useTranslation("reportCard");
	const seen = seenPatterns(group);
	if (seen.length === 0) {
		// "No pattern" over a failed run would contradict the failures listed below it (M3-6).
		return (
			<p className="text-muted-foreground text-sm" data-testid={testId ? `${testId}-none` : undefined}>
				{(group.failed ?? 0) > 0 ? tr("failures.noRepeated", { count: group.failed }) : t("failures.noPatterns", { count: group.runs })}
			</p>
		);
	}
	return (
		<ul className="flex flex-col divide-y rounded-lg border" data-testid={testId}>
			{seen.map((p) => (
				<li key={p.pattern} className="grid gap-2 px-4 py-3 sm:grid-cols-[12rem_6rem_minmax(0,1fr)] sm:items-start" data-testid={`pattern-${p.pattern}`}>
					<span className="flex flex-col gap-0.5">
						<span className="text-sm font-medium">{t(`failures.pattern.${p.pattern}`, { defaultValue: p.label })}</span>
						<span className="text-muted-foreground text-xs">{p.description}</span>
					</span>
					<span className="text-sm tabular-nums">
						<span className="font-semibold">{pct(patternRate(p))}</span>
						<span className="text-muted-foreground block text-xs">{t("failures.ofRuns", { count: p.count, total: p.observed })}</span>
					</span>
					<span className="flex min-w-0 flex-col gap-1">
						{p.examples.map((ex) => (
							<ExampleRunLink key={ex.run_id} example={ex} />
						))}
					</span>
				</li>
			))}
		</ul>
	);
}

/** The most common failures across the report's experiments (failure groups), with example runs. */
function TopClusters({ clusters }: { clusters: readonly ClusterSummary[] }) {
	const { t } = useTranslation("agentFirst");
	const { t: tr } = useTranslation("reportCard");
	return (
		<ul className="flex flex-col divide-y rounded-lg border" data-testid="report-failure-clusters">
			{clusters.map((c) => (
				<li key={c.signature} className="flex flex-col gap-1 px-4 py-3" data-testid={`report-failure-cluster-${c.signature}`}>
					<span className="flex flex-wrap items-baseline gap-x-2">
						<span className="text-sm font-medium">{c.title}</span>
						<span className="text-muted-foreground text-xs tabular-nums">{t("failures.clusterRuns", { count: c.count })}</span>
					</span>
					<span className="flex flex-wrap gap-x-3 gap-y-1">
						{/* Plain link text; the run id is in the title (X-25). The cluster payload has no task names yet. */}
						{(c.examples ?? []).map((runId, i) => (
							<Link
								key={runId}
								to="/runs/$runId"
								params={{ runId }}
								title={runId}
								className="text-primary text-sm underline-offset-4 hover:underline"
							>
								{tr("failures.runN", { n: i + 1 })}
							</Link>
						))}
					</span>
				</li>
			))}
		</ul>
	);
}

/**
 * "Where it failed" on a report card: per arm, the failure patterns (loops, false done, ignored
 * errors) with example runs, then the most common failure groups across the report's benchmarks.
 * Shows nothing until the report has a run.
 */
export function ReportFailures({ report, agent }: { report: Report; agent?: string }) {
	const { t } = useTranslation("agentFirst");
	const experiments = reportExperiments(report);
	const patterns = useGetEvaluationFailurePatternsQuery(report.evaluation ? report.evaluation : skipToken);
	const clusters = useGetClustersQuery(experiments.length > 0 ? { exp: experiments.join(","), agent } : skipToken);
	const groups = groupsForReport(patterns.data?.groups ?? [], report).filter((g) => g.runs > 0);
	const top = (clusters.data?.clusters ?? []).slice(0, REPORT_CLUSTERS_SHOWN);
	if (!report.evaluation || (groups.length === 0 && top.length === 0)) return null;
	return (
		<Section title={t("failures.title")} description={t("failures.description")} testId="report-failures">
			<div className="flex min-w-0 flex-col gap-5">
				{groups.map((g) => (
					<div key={g.key} className="flex min-w-0 flex-col gap-2">
						{groups.length > 1 && (
							<h3 className="text-sm font-semibold">
								{g.key}
								<span className="text-muted-foreground ml-2 font-normal">{g.version ? `${g.agent} v${g.version}` : g.agent}</span>
							</h3>
						)}
						<PatternList group={g} testId={`report-failure-patterns-${g.key}`} />
					</div>
				))}
				{top.length > 0 && (
					<div className="flex min-w-0 flex-col gap-2">
						<h3 className="text-sm font-semibold">{t("failures.topGroups")}</h3>
						<TopClusters clusters={top} />
					</div>
				)}
				<Link
					to={"/why-agents-fail" as never}
					search={(agent ? { agent } : {}) as never}
					className="text-primary inline-flex w-fit items-center gap-1 text-sm font-medium underline-offset-4 hover:underline"
					data-testid="report-failures-more"
				>
					{t("failures.more")}
					<ArrowUpRight className="size-3.5" aria-hidden="true" />
				</Link>
			</div>
		</Section>
	);
}
