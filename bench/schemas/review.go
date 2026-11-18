package schemas

import (
	"errors"
	"time"
)

// Human review (docs/human-review.md). A review is a person's decision about
// one sealed run, written as a child artifact beside the run the way a
// regrade is: <exp>/reviews/<review_id>/review.json plus one line in the
// experiment's append-only, hash-chained reviews.log. A review never edits
// the sealed run, never deletes or replaces another review, and is applied
// only when an analysis asks for grade source "reviewed".

// GradeSourceReviewed is the grade source that applies human reviews on top
// of the sealed original grades. It is reserved: no regrade may use the id.
const GradeSourceReviewed = "reviewed"

// ErrReviewInput marks a review that cannot be written as asked: a missing
// reason or reviewer, an unknown decision, or a run that is not sealed. The
// API answers it with 400.
var ErrReviewInput = errors.New("invalid review")

// EvidenceSourceHumanReview marks a value that a human review decided.
const EvidenceSourceHumanReview EvidenceOrigin = "human_review"

// EvidenceReviewed is the evidence strength of a value a human review
// decided. It is never "verified": no verifier backs it.
const EvidenceReviewed EvidenceStrength = "reviewed"

// ReviewDecision is what the reviewer decided about the run.
type ReviewDecision string

// Review decisions. pass, fail and invalid replace the run's outcome under
// grade source reviewed; flag_only records a concern and changes nothing.
const (
	ReviewPass     ReviewDecision = "pass"      // the run succeeded; also overrules a fired veto gate
	ReviewFail     ReviewDecision = "fail"      // the run did not succeed
	ReviewInvalid  ReviewDecision = "invalid"   // the evidence cannot decide; the run counts as unknown
	ReviewFlagOnly ReviewDecision = "flag_only" // a concern for others to look at; the outcome stays
)

// Valid reports whether d is one of the four decisions.
func (d ReviewDecision) Valid() bool {
	switch d {
	case ReviewPass, ReviewFail, ReviewInvalid, ReviewFlagOnly:
		return true
	}
	return false
}

// Outcome is the run outcome the decision sets, and false for flag_only.
func (d ReviewDecision) Outcome() (Outcome, bool) {
	switch d {
	case ReviewPass:
		return OutcomePass, true
	case ReviewFail:
		return OutcomeFail, true
	case ReviewInvalid:
		return OutcomeInvalidEvaluator, true
	}
	return "", false
}

// Review is <exp>/reviews/<review_id>/review.json. Hash is the sha256 of the
// canonical JSON with Hash empty; PrevHash is the hash on the reviews.log
// line before this review's line, so the records form one chain. Both are
// checked on every read, together with ParentSeal against the run's seal.
type Review struct {
	ReviewID        string         `json:"review_id"`
	ExpID           string         `json:"exp_id"`
	RunID           string         `json:"run_id"`
	ParentSeal      string         `json:"parent_seal"` // the run's seal hash when the review was written
	Decision        ReviewDecision `json:"decision"`
	Reason          string         `json:"reason"`   // plain text, required
	Reviewer        string         `json:"reviewer"` // self-declared name; there is no authentication
	CreatedAt       time.Time      `json:"created_at"`
	SealedOutcome   Outcome        `json:"sealed_outcome"`       // the outcome the run was sealed with
	PreviousOutcome Outcome        `json:"previous_outcome"`     // the outcome in effect before this review: the sealed one or the superseded review's
	Supersedes      string         `json:"supersedes,omitempty"` // the earlier decision review of the run this one replaces
	Evidence        []string       `json:"evidence,omitempty"`   // what the reviewer cited: files, gate names, judge votes, free text
	PrevHash        string         `json:"prev_hash"`            // hash on the previous reviews.log line, "" for the first
	Hash            string         `json:"hash"`
}

// ReviewRequest is POST /reviews and the input of `bifrost-bench review add`.
type ReviewRequest struct {
	ExpID    string         `json:"exp_id,omitempty"` // optional; the run id alone locates the run
	RunID    string         `json:"run_id"`
	Decision ReviewDecision `json:"decision"`
	Reason   string         `json:"reason"`
	Reviewer string         `json:"reviewer"`
	Evidence []string       `json:"evidence,omitempty"`
}

// Review queue reasons: why a run needs a person to look at it.
const (
	ReviewReasonSplitVotes       = "split_votes"       // a judged grade whose judge calls disagreed
	ReviewReasonUnparsableVotes  = "unparsable_votes"  // a judged grade with a judge call that could not be read
	ReviewReasonInvalidEvaluator = "invalid_evaluator" // the grader could not decide
	ReviewReasonVeto             = "veto"              // a veto gate fired
	ReviewReasonFalseCompletion  = "false_completion"  // the agent claimed success on a failed run
	ReviewReasonFlagged          = "flagged"           // a flag_only review asked for a look
	ReviewReasonContested        = "contested"         // W8 judge calibration: split votes, an auditor or a human review disagreeing with the judge
)

// ReviewReasons lists the queue reasons in display order.
var ReviewReasons = []string{
	ReviewReasonSplitVotes, ReviewReasonUnparsableVotes, ReviewReasonInvalidEvaluator,
	ReviewReasonVeto, ReviewReasonFalseCompletion, ReviewReasonFlagged,
	ReviewReasonContested, // W8 judge calibration
}

// ReviewQueueItem is one run the queue asks a person to look at.
type ReviewQueueItem struct {
	RunID         string         `json:"run_id"`
	ExpID         string         `json:"exp_id"`
	PairID        string         `json:"pair_id"`
	Task          string         `json:"task"`
	Arm           string         `json:"arm"`
	Condition     string         `json:"condition"`
	Trial         int            `json:"trial"`
	Reasons       []string       `json:"reasons"`
	SealedOutcome Outcome        `json:"sealed_outcome"`
	Outcome       Outcome        `json:"outcome"`            // the outcome in effect: the newest valid decision review's, else the sealed one
	Reviews       int            `json:"reviews"`            // valid reviews of the run, flags included
	Decision      ReviewDecision `json:"decision,omitempty"` // the newest valid decision review's, empty when none
}

// ReviewQueue is GET /experiments/{id}/reviews/queue and `review list`.
type ReviewQueue struct {
	ExpID    string            `json:"exp_id"`
	Items    []ReviewQueueItem `json:"items"`
	Reasons  []string          `json:"reasons"`  // every queue reason, for the filter
	Problems []string          `json:"problems"` // reviews ignored because they do not verify, runs whose seal does not verify
}

// RunReviews is GET /runs/{id}/reviews and `review show`: every valid review
// of the run in log order, oldest first, and the one in effect.
type RunReviews struct {
	RunID         string   `json:"run_id"`
	ExpID         string   `json:"exp_id"`
	SealedOutcome Outcome  `json:"sealed_outcome"`
	Outcome       Outcome  `json:"outcome"`           // the outcome under grade source reviewed
	Current       *Review  `json:"current,omitempty"` // the newest valid decision review, nil when none
	Reviews       []Review `json:"reviews"`
	Reasons       []string `json:"reasons"`  // queue reasons that apply to the run, empty when none
	Problems      []string `json:"problems"` // reviews of this run that were ignored, and why
}

// ReviewLog is GET /experiments/{id}/reviews and `review log`: the whole
// chain in log order and whether it verifies.
type ReviewLog struct {
	ExpID    string   `json:"exp_id"`
	Reviews  []Review `json:"reviews"`
	Verified bool     `json:"verified"` // every line well formed, every record present and matching, the chain unbroken
	Problems []string `json:"problems"`
}
