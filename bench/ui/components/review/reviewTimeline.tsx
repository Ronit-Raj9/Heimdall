import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { EmptyNote } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Review, RunReviews } from "@/lib/types/bench";
import type { TFunction } from "i18next";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { reviewState, shortReviewHash, type ReviewState } from "./review.utils";

const STATE_VARIANT: Record<ReviewState, "default" | "outline" | "warning"> = {
	current: "default",
	superseded: "outline",
	flag: "warning",
};

export interface ReviewTimelineProps {
	data: RunReviews;
	/** Prefix of every data-testid, so two runs on one page stay apart. */
	testId?: string;
}

/**
 * A run's reviews in log order, oldest first: who decided what and why, what each one replaced,
 * and which decision is in effect. Reviews that failed their checks are listed apart and never
 * applied. Reason and evidence are inert text.
 */
export function ReviewTimeline({ data, testId = "review-timeline" }: ReviewTimelineProps) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col gap-3" data-testid={testId}>
			<dl className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
				<div className="flex items-center gap-2">
					<dt className="text-muted-foreground text-xs">{t("review.timeline.sealed")}</dt>
					<dd>
						<OutcomeBadge outcome={data.sealed_outcome} testId={`${testId}-sealed`} />
					</dd>
				</div>
				<div className="flex items-center gap-2">
					<dt className="text-muted-foreground text-xs">{t("review.timeline.effective")}</dt>
					<dd>
						<OutcomeBadge outcome={data.outcome} testId={`${testId}-effective`} />
					</dd>
				</div>
			</dl>
			{data.reviews.length === 0 ? (
				<EmptyNote testId={`${testId}-empty`}>{t("review.timeline.empty")}</EmptyNote>
			) : (
				<ol className="flex flex-col gap-3 border-l pl-4">
					{data.reviews.map((review) => (
						<TimelineEntry key={review.review_id} review={review} state={reviewState(review, data.current)} testId={testId} />
					))}
				</ol>
			)}
			{data.problems.length > 0 && (
				<div
					className="border-destructive/40 bg-destructive/5 flex flex-col gap-1 rounded-sm border px-3 py-2 text-sm"
					data-testid={`${testId}-problems`}
				>
					<p className="flex items-center gap-2 font-medium">
						<AlertTriangle className="text-chart-error-ink size-4" aria-hidden="true" />
						{t("review.timeline.problems")}
					</p>
					<ul className="text-muted-foreground list-disc pl-5 break-words">
						{data.problems.map((problem) => (
							<li key={problem}>{problem}</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

function TimelineEntry({ review, state, testId }: { review: Review; state: ReviewState; testId: string }) {
	const { t } = useTranslation("bench");
	const time = formatDateTime(review.created_at, getLocale()) ?? review.created_at;
	return (
		<li
			className={cn("relative flex flex-col gap-1.5 text-sm", state === "superseded" && "opacity-70")}
			data-testid={`${testId}-entry-${review.review_id}`}
			data-state={state}
		>
			<span className="bg-border absolute top-1.5 -left-[21px] size-2.5 rounded-full border" aria-hidden="true" />
			<div className="flex flex-wrap items-center gap-2">
				<Badge variant="outline">{t(`review.decision.${review.decision}`)}</Badge>
				<Badge variant={STATE_VARIANT[state]} data-testid={`${testId}-state-${review.review_id}`}>
					{t(`review.timeline.${state}`)}
				</Badge>
				<span className="text-muted-foreground text-xs">{t("review.timeline.by", { reviewer: review.reviewer, time })}</span>
				<span className="text-muted-foreground font-mono text-xs">{review.review_id}</span>
			</div>
			{review.decision !== "flag_only" && (
				<p className="text-muted-foreground text-xs" data-testid={`${testId}-change-${review.review_id}`}>
					{t("review.timeline.change", { from: t(`runs.outcome.${review.previous_outcome}`), to: decisionOutcomeLabel(review, t) })}
				</p>
			)}
			<p className="break-words whitespace-pre-wrap">{review.reason}</p>
			{review.supersedes && <p className="text-muted-foreground text-xs">{t("review.timeline.supersedes", { id: review.supersedes })}</p>}
			{review.evidence && review.evidence.length > 0 && (
				<div className="flex flex-wrap items-center gap-1.5 text-xs">
					<span className="text-muted-foreground">{t("review.timeline.evidence")}</span>
					{review.evidence.map((item) => (
						<code key={item} className="bg-muted rounded-sm px-1 py-0.5 break-all">
							{item}
						</code>
					))}
				</div>
			)}
			<p className="text-muted-foreground font-mono text-[11px]">{t("review.timeline.hash", { hash: shortReviewHash(review.hash) })}</p>
		</li>
	);
}

/** The outcome a decision sets, in words. */
function decisionOutcomeLabel(review: Review, t: TFunction<"bench">): string {
	switch (review.decision) {
		case "pass":
			return t("runs.outcome.pass");
		case "fail":
			return t("runs.outcome.fail");
		case "invalid":
			return t("runs.outcome.invalid_evaluator");
		default:
			return t(`runs.outcome.${review.previous_outcome}`);
	}
}