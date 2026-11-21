// Temporary stub: lane A15 owns the real report card. Same import path and props, so the
// evaluation page needs no change when the real one lands.
import { Link } from "@tanstack/react-router";

export interface ReportCardProps {
	/** The evaluation whose report (GET /evaluations/{id}/report) to show. */
	evaluationId: string;
}

export function ReportCard({ evaluationId }: ReportCardProps) {
	return (
		<section className="bg-card rounded-xl border p-5 text-sm" data-testid="report-card" data-evaluation={evaluationId}>
			<h2 className="text-base font-semibold">Report card</h2>
			<p className="text-muted-foreground mt-1">
				The report card is not built yet. Open each benchmark's comparison above for its scores, or the{" "}
				<Link to="/leaderboard" className="underline">
					leaderboard
				</Link>
				.
			</p>
		</section>
	);
}

export default ReportCard;