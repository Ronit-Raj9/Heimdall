import { ReviewForm } from "@/components/review/reviewForm";
import { EntityLink } from "@/components/shell";
import { RunEvidenceSummary } from "@/components/review/runEvidenceSummary";
import { RunReviewsBlock } from "@/components/review/runReviewsBlock";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getBenchErrorMessage, useCreateReviewMutation, useGetRunDiffQuery, useGetRunQuery } from "@/lib/store/apis/benchApi";
import type { ReviewQueueItem } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/**
 * One queued run opened for review: its sealed evidence, its review timeline and the form. The
 * form is keyed by run, so switching runs starts a fresh form.
 */
export function ReviewRunPanel({ item }: { item: ReviewQueueItem }) {
	const { t } = useTranslation("bench");
	const detail = useGetRunQuery(item.run_id);
	// An answer task has no diff worth showing; ask for it only when the run wrote no answer.
	const wantsDiff = detail.data !== undefined && !detail.data.answer;
	const diff = useGetRunDiffQuery(wantsDiff ? item.run_id : skipToken);
	const [createReview, created] = useCreateReviewMutation();

	return (
		<div className="flex flex-col gap-4" data-testid="review-run-panel" data-run-id={item.run_id}>
			<Card className="gap-4">
				<CardHeader>
					<div className="flex flex-wrap items-center justify-between gap-2">
						<CardTitle className="text-sm">
							<EntityLink kind="run" id={item.run_id} testId="review-run-link">
								<span className="font-mono">{t("review.panel.title", { id: item.run_id })}</span>
							</EntityLink>
						</CardTitle>
						<Button variant="outline" size="sm" asChild>
							<Link to="/runs" search={{ pair: item.pair_id, exp: item.exp_id }} data-testid="review-open-pair">
								{t("review.panel.openPair")}
							</Link>
						</Button>
					</div>
					<CardDescription className="font-mono text-xs">
						{item.task} / {item.arm} / {item.condition} / {item.trial}
					</CardDescription>
				</CardHeader>
				<CardContent>
					{detail.isLoading ? (
						<BlockSkeleton rows={5} testId="review-run-loading" />
					) : detail.error || !detail.data ? (
						<LoadError
							title={t("review.panel.loadError", { id: item.run_id })}
							error={detail.error}
							onRetry={() => void detail.refetch()}
							testId="review-run-error"
						/>
					) : (
						<RunEvidenceSummary detail={detail.data} diff={diff.data} />
					)}
				</CardContent>
			</Card>

			<Card className="gap-4">
				<CardHeader>
					<CardTitle>{t("review.timeline.title")}</CardTitle>
					<CardDescription>{t("review.timeline.description")}</CardDescription>
				</CardHeader>
				<CardContent>
					<RunReviewsBlock runId={item.run_id} testId="review-run-timeline" />
				</CardContent>
			</Card>

			<Card className="gap-4">
				<CardHeader>
					<CardTitle>{t("review.form.title")}</CardTitle>
				</CardHeader>
				<CardContent>
					<ReviewForm
						key={item.run_id}
						runId={item.run_id}
						expId={item.exp_id}
						mode={detail.data?.mode}
						submitting={created.isLoading}
						submitError={created.isError ? getBenchErrorMessage(created.error) : undefined}
						created={created.data?.run_id === item.run_id ? created.data : undefined}
						onSubmit={(request) => void createReview(request)}
					/>
				</CardContent>
			</Card>
		</div>
	);
}