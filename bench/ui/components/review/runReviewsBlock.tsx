import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetRunReviewsQuery } from "@/lib/store/apis/benchApi";
import { useTranslation } from "react-i18next";
import { ReviewTimeline } from "./reviewTimeline";

/** One run's review timeline, fetched from GET /runs/{id}/reviews. */
export function RunReviewsBlock({ runId, testId = "run-reviews" }: { runId: string; testId?: string }) {
	const { t } = useTranslation("bench");
	const reviews = useGetRunReviewsQuery(runId);
	if (reviews.isLoading) return <BlockSkeleton rows={2} testId={`${testId}-loading`} />;
	if (reviews.error || !reviews.data) {
		return (
			<LoadError
				title={t("review.timeline.loadError")}
				error={reviews.error}
				onRetry={() => void reviews.refetch()}
				testId={`${testId}-error`}
			/>
		);
	}
	return <ReviewTimeline data={reviews.data} testId={testId} />;
}