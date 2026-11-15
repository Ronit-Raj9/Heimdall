// Pure helpers for the human review screens: form validation and request building, queue
// filtering, and how one review sits in a run's history. No I/O, so each is tested alone.
import {
	REVIEW_DECISIONS,
	REVIEW_REASONS,
	type Mode,
	type Review,
	type ReviewDecision,
	type ReviewQueueItem,
	type ReviewReason,
	type ReviewRequest,
} from "@/lib/types/bench";

/** The review form's fields as typed. decision is "" until one is picked. */
export interface ReviewFormValues {
	decision: ReviewDecision | "";
	reason: string;
	reviewer: string;
	/** One cited item per line. */
	evidence: string;
}

export const EMPTY_REVIEW_FORM: ReviewFormValues = { decision: "", reason: "", reviewer: "", evidence: "" };

/** A form field that blocks submitting. */
export type ReviewFormError = "decision" | "reason" | "reviewer";

/** True for a reason the queue knows; the server may add one the UI does not, which then shows raw. */
export function isReviewReason(value: string): value is ReviewReason {
	return (REVIEW_REASONS as readonly string[]).includes(value);
}

/** True for one of the four decisions. */
export function isReviewDecision(value: string): value is ReviewDecision {
	return (REVIEW_DECISIONS as readonly string[]).includes(value);
}

/** The fields still missing: a decision, a reason and a reviewer name are all required. */
export function reviewFormErrors(values: ReviewFormValues): ReviewFormError[] {
	const errors: ReviewFormError[] = [];
	if (!isReviewDecision(values.decision)) errors.push("decision");
	if (values.reason.trim() === "") errors.push("reason");
	if (values.reviewer.trim() === "") errors.push("reviewer");
	return errors;
}

/** Only a live server takes reviews; recorded and synthetic run folders refuse every write. */
export function canWriteReviews(mode: Mode | undefined): boolean {
	return mode === "live";
}

/** The POST /reviews body from valid form values: text trimmed, one evidence item per non-empty line. */
export function reviewRequestFrom(runId: string, expId: string | undefined, values: ReviewFormValues): ReviewRequest {
	if (!isReviewDecision(values.decision)) throw new Error("review request needs a decision");
	const evidence = values.evidence
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line.length > 0);
	return {
		...(expId ? { exp_id: expId } : {}),
		run_id: runId,
		decision: values.decision,
		reason: values.reason.trim(),
		reviewer: values.reviewer.trim(),
		...(evidence.length > 0 ? { evidence } : {}),
	};
}

/** Queue items with the reason, or every item when reason is empty. */
export function filterQueue(items: readonly ReviewQueueItem[], reason: string | null | undefined): ReviewQueueItem[] {
	if (!reason) return [...items];
	return items.filter((item) => item.reasons.includes(reason));
}

/** How a review sits in its run's history: the decision in effect, an older decision, or a flag. */
export type ReviewState = "current" | "superseded" | "flag";

export function reviewState(review: Review, current: Review | undefined): ReviewState {
	if (review.decision === "flag_only") return "flag";
	return current?.review_id === review.review_id ? "current" : "superseded";
}

/** Judge votes counted by verdict; anything else counts as unparsable. */
export interface VoteTally {
	correct: number;
	incorrect: number;
	unparsable: number;
}

export function tallyVotes(votes: readonly string[] | undefined): VoteTally {
	const tally: VoteTally = { correct: 0, incorrect: 0, unparsable: 0 };
	for (const vote of votes ?? []) {
		if (vote === "correct") tally.correct++;
		else if (vote === "incorrect") tally.incorrect++;
		else tally.unparsable++;
	}
	return tally;
}

/** A short, stable form of a hash for display: the first 12 hex digits after any "sha256:" prefix. */
export function shortReviewHash(hash: string): string {
	const hex = hash.startsWith("sha256:") ? hash.slice("sha256:".length) : hash;
	return hex.slice(0, 12);
}