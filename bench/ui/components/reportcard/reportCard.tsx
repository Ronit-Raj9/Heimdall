import type { ReportCardResponse } from "@/lib/store/apis/evaluationsApi";

/**
 * Temporary stand-in for the report card (lane A15 owns this file and replaces it). Shows each
 * capability score per arm as plain text so the agent page works before A15 lands.
 */
export function ReportCard({ report }: { report: ReportCardResponse }) {
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="report-card">
			{report.overall?.summary && <p className="text-sm font-medium">{report.overall.summary}</p>}
			<ul className="flex flex-col gap-1 text-sm">
				{report.capabilities.map((cap) => (
					<li key={cap.capability} data-testid={`report-card-cap-${cap.capability}`}>
						<span className="font-medium">{cap.capability}</span>
						{Object.entries(cap.arms).map(([arm, m]) => (
							<span key={arm} className="text-muted-foreground ml-3 tabular-nums">
								{arm}: {Math.round(m.score * 100)}% ({m.runs} runs)
							</span>
						))}
					</li>
				))}
			</ul>
		</div>
	);
}

export default ReportCard;
