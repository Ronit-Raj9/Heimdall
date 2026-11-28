import "./ufl2I18n";

import { BlockSkeleton, EmptyNote, InlineError } from "@/components/runs/screenStates";
import { useGetReviewLogQuery } from "@/lib/store/apis/experimentOverheadApi";
import { useTranslation } from "react-i18next";

/** GET /experiments/{id}/reviews: every review of the comparison and whether the hash chain verifies. Folded. */
export function ReviewLogPanel({ expId }: { expId: string }) {
	const { t } = useTranslation("ufl2");
	const q = useGetReviewLogQuery(expId);
	const reviews = q.data?.reviews ?? [];
	return (
		<details className="rounded-lg border px-4 py-3" data-testid="review-log">
			<summary className="cursor-pointer text-sm font-semibold">
				{t("reviews.title")}
				{q.data && reviews.length > 0 && (
					<span className={q.data.verified ? "text-muted-foreground ml-2 text-xs font-normal" : "text-chart-error-ink ml-2 text-xs font-normal"} data-testid="review-log-verified">
						{q.data.verified ? t("reviews.verified") : t("reviews.broken")}
					</span>
				)}
			</summary>
			<div className="mt-2">
				{q.isLoading ? (
					<BlockSkeleton rows={2} testId="review-log-loading" />
				) : q.isError ? (
					<InlineError error={q.error} title={t("reviews.error")} onRetry={() => void q.refetch()} testId="review-log-error" />
				) : reviews.length === 0 ? (
					<EmptyNote testId="review-log-empty">{t("reviews.empty")}</EmptyNote>
				) : (
					<div className="overflow-x-auto">
						<table className="w-full text-xs" data-testid="review-log-table">
							<thead className="text-muted-foreground text-left">
								<tr>
									<th className="py-1 pr-3 font-medium">{t("reviews.when")}</th>
									<th className="py-1 pr-3 font-medium">{t("reviews.run")}</th>
									<th className="py-1 pr-3 font-medium">{t("reviews.decision")}</th>
									<th className="py-1 font-medium">{t("reviews.who")}</th>
								</tr>
							</thead>
							<tbody>
								{reviews.map((r) => (
									<tr key={r.review_id} className="border-t" title={r.reason}>
										<td className="py-1 pr-3 whitespace-nowrap tabular-nums">{new Date(r.created_at).toLocaleString()}</td>
										<td className="py-1 pr-3 font-mono">{r.run_id}</td>
										<td className="py-1 pr-3">{r.decision}</td>
										<td className="py-1">{r.reviewer}</td>
									</tr>
								))}
							</tbody>
						</table>
						{(q.data?.problems ?? []).length > 0 && (
							<ul className="text-chart-error-ink mt-2 list-disc pl-4 text-xs">
								{q.data?.problems?.map((p) => <li key={p}>{p}</li>)}
							</ul>
						)}
					</div>
				)}
			</div>
		</details>
	);
}
