import { budgetStopOf, RaiseBudgetPanel } from "@/app/evaluations_/$evalId/views/raiseBudget";
import { type Evaluation, type Report, useGetEvaluationReportQuery } from "@/lib/store/apis/evaluationsApi";
import { useGetEvaluationFailurePatternsQuery } from "@/lib/store/apis/failurePatternsApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";
import { ReportCard } from "./reportCard";
import "./reportCardI18n";
import { ReportFailures } from "./reportFailures";

/** Counted runs of a report: the server's done count, else the sum over benchmark rows. */
export function reportDoneRuns(report: Report): number {
	if (typeof report.done_runs === "number") return report.done_runs;
	return report.benchmarks.reduce((acc, r) => acc + Object.values(r.arms).reduce((n, m) => n + (m?.runs ?? 0), 0), 0);
}

/** The first failed example run of the report's failure patterns (M3-18), or undefined. */
function useFailedRun(evaluationId: string): string | undefined {
	const patterns = useGetEvaluationFailurePatternsQuery(evaluationId ? evaluationId : skipToken);
	for (const g of patterns.data?.groups ?? []) {
		for (const p of g.patterns ?? []) {
			const ex = p.examples?.[0];
			if (ex?.run_id) return ex.run_id;
		}
	}
	return undefined;
}

/**
 * Fetches GET /evaluations/{id}/report and shows it as a report card. With the evaluation, a budget stop leads the
 * headline and the raise budget panel sits right under it (M3-4); a link opens a failed run's timeline (M3-18).
 */
export function EvaluationReportCard({ evaluationId, evaluation }: { evaluationId: string; evaluation?: Evaluation }) {
	const { t } = useTranslation("reportCard");
	const [where, setWhere] = React.useState<Record<string, string> | undefined>();
	const report = useGetEvaluationReportQuery(where ? { id: evaluationId, where } : evaluationId);
	const failedRun = useFailedRun(evaluationId);
	// The raise panel never waits for the report: a budget stop is shown even while it loads or fails (M3-4).
	const panel = evaluation ? <RaiseBudgetPanel evaluation={evaluation} /> : null;
	if (report.isLoading) {
		return (
			<>
				{panel}
				<p className="text-muted-foreground text-sm" data-testid="report-card-loading">
					Loading the report card...
				</p>
			</>
		);
	}
	if (report.error || !report.data) {
		return (
			<>
			{panel}
			<p className="text-muted-foreground text-sm" data-testid="report-card-error">
				Could not load the report card.{" "}
				<button type="button" className="text-primary underline" onClick={() => void report.refetch()}>
					Try again
				</button>
			</p>
			</>
		);
	}
	const budgetStop = evaluation ? budgetStopOf(evaluation, reportDoneRuns(report.data)) : undefined;
	const below =
		budgetStop || failedRun ? (
			<div className="flex flex-col gap-3">
				{evaluation && budgetStop && <RaiseBudgetPanel evaluation={evaluation} />}
				{failedRun && (
					<Link
						to="/runs/$runId"
						params={{ runId: failedRun }}
						search={{ tab: "timeline" } as never}
						className="text-primary inline-flex w-fit items-center gap-1 text-sm font-medium underline-offset-4 hover:underline"
						data-testid="report-card-failed-run"
					>
						{t("links.failedRun")}
						<ArrowUpRight className="size-3.5" aria-hidden="true" />
					</Link>
				)}
			</div>
		) : undefined;
	return (
		<div className="flex min-w-0 flex-col gap-8">
			<ReportCard report={report.data} onWhere={setWhere} headlineContext={{ budgetStop }} belowHeadline={below} />
			<ReportFailures report={report.data} />
		</div>
	);
}

export default EvaluationReportCard;
