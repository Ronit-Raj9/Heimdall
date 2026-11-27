import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
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
	// M5-8, M4-59: the outcome pills mean something only once a review exists; with none, one short line.
	if (reviews.data.reviews.length === 0 && reviews.data.problems.length === 0) {
		return <EmptyNote testId={`${testId}-empty`}>{t("review.timeline.empty")}</EmptyNote>;
	}
	return <ReviewTimeline data={reviews.data} testId={testId} />;
}