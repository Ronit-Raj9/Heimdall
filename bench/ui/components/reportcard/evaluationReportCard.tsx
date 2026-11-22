import { useGetEvaluationReportQuery } from "@/lib/store/apis/evaluationsApi";
import * as React from "react";
import { ReportCard } from "./reportCard";
import { ReportFailures } from "./reportFailures";

/** Fetches GET /evaluations/{id}/report and shows it as a report card. */
export function EvaluationReportCard({ evaluationId }: { evaluationId: string }) {
	const [where, setWhere] = React.useState<Record<string, string> | undefined>();
	const report = useGetEvaluationReportQuery(where ? { id: evaluationId, where } : evaluationId);
	if (report.isLoading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="report-card-loading">
				Loading the report card...
			</p>
		);
	}
	if (report.error || !report.data) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="report-card-error">
				Could not load the report card.{" "}
				<button type="button" className="text-primary underline" onClick={() => void report.refetch()}>
					Try again
				</button>
			</p>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-8">
			<ReportCard report={report.data} onWhere={setWhere} />
			<ReportFailures report={report.data} />
		</div>
	);
}

export default EvaluationReportCard;
