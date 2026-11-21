import { useGetEvaluationReportQuery } from "@/lib/store/apis/evaluationsApi";
import { ReportCard } from "./reportCard";

/** Fetches GET /evaluations/{id}/report and shows it as a report card. */
export function EvaluationReportCard({ evaluationId }: { evaluationId: string }) {
	const report = useGetEvaluationReportQuery(evaluationId);
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
	return <ReportCard report={report.data} />;
}

export default EvaluationReportCard;
