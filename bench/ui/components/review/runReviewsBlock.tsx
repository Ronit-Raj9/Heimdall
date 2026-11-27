import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import "@/components/runs/uf5aI18n";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage, useCreateReviewMutation, useGetRunReviewsQuery } from "@/lib/store/apis/benchApi";
import type { Mode } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ReviewForm } from "./reviewForm";
import { ReviewTimeline } from "./reviewTimeline";

/**
 * One run's review timeline, fetched from GET /runs/{id}/reviews. With `add`, an empty timeline offers
 * "Add a review", which opens the review form in place (X-41); the review queue has its own form.
 */
export function RunReviewsBlock({
	runId,
	testId = "run-reviews",
	add,
}: {
	runId: string;
	testId?: string;
	add?: { expId?: string; mode?: Mode };
}) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf5a");
	const reviews = useGetRunReviewsQuery(runId);
	const [adding, setAdding] = useState(false);
	const [createReview, created] = useCreateReviewMutation();
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
		return (
			<div className="flex flex-col gap-3">
				<EmptyNote testId={`${testId}-empty`}>{t("review.timeline.empty")}</EmptyNote>
				{add && !adding && (
					<Button variant="outline" size="sm" className="self-start" onClick={() => setAdding(true)} dataTestId={`${testId}-add`}>
						{tu("reviews.add")}
					</Button>
				)}
				{add && adding && (
					<ReviewForm
						runId={runId}
						expId={add.expId}
						mode={add.mode}
						submitting={created.isLoading}
						submitError={created.isError ? getBenchErrorMessage(created.error) : undefined}
						created={created.data?.run_id === runId ? created.data : undefined}
						onSubmit={(request) => void createReview(request)}
					/>
				)}
			</div>
		);
	}
	return <ReviewTimeline data={reviews.data} testId={testId} />;
}
