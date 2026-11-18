package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/calibration" // W8 judge calibration: contested runs
	"github.com/maximhq/bifrost/bench/schemas"
)

// Metric names a human review decides, as the metrics package names them.
// They are the per-run metrics that are a pure function of the grade, the
// fired veto gates and the agent's claim, so they can be recomputed here
// from the reviewed decision without the registry; a test checks the
// formulas against the metrics package. Every other metric (cost, steps,
// tokens, the verifier's own reward) is evidence the reviewer did not
// decide and stays as sealed.
const (
	reviewMetricVerified       = "verified_success"
	reviewMetricSafe           = "safe_success"
	reviewMetricAnswer         = "answer_correct"
	reviewMetricAnswerCategory = "answer_" // answer_<category>, absent when not observed
	reviewMetricFalseComplete  = "false_completion"
	reviewMetricClaimPrecision = "claim_precision"
	reviewMetricCheating       = "cheating_rate"
)

// reviewedRuns is SealedRuns under grade source reviewed: every counted run
// as sealed, then, for each run whose seal verifies and that has an
// effective review (EffectiveReview), the review's decision replaces the
// outcome, Y, the grade's pass and the metrics it decides (applyReview).
// Reviews that do not verify are ignored and logged; ReviewQueue,
// RunReviews and ReviewLog report them. Only the in-memory copy changes.
func (s *FSStore) reviewedRuns(ctx context.Context, expID string) ([]schemas.SealedRun, error) {
	runs, err := s.SealedRuns(ctx, expID, GradeSourceOriginal)
	if err != nil {
		return nil, err
	}
	set, err := s.Reviews(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	for _, p := range set.Problems {
		s.logger.Warn("review ignored", "exp_id", expID, "problem", p)
	}
	for i := range runs {
		sr := &runs[i]
		if len(sr.Problems) > 0 {
			continue
		}
		rv, problems := EffectiveReview(set.ForRun(sr.Run.RunID), sr.SealHash)
		for _, p := range problems {
			s.logger.Warn("review ignored", "exp_id", expID, "problem", p)
		}
		if rv != nil {
			applyReview(sr, rv)
		}
	}
	return runs, nil
}

// ReviewCounts returns how many runs took their outcome from a human review
// and how many of those the review changed from the sealed outcome.
func ReviewCounts(runs []schemas.SealedRun) (reviewed, changed int) {
	for _, r := range runs {
		if r.Review == nil || !r.Slot.Counted {
			continue
		}
		reviewed++
		if r.Review.SealedOutcome != r.Run.Outcome {
			changed++
		}
	}
	return reviewed, changed
}

// ReviewQueue lists the counted, sealed runs of an experiment that need a
// person to look at them (QueueReasons), read with their sealed grades, in
// slot order. reason, when set, keeps only runs with that reason. A run whose
// seal does not verify is not reviewable and is named in Problems, with every
// review that was ignored.
func (s *FSStore) ReviewQueue(ctx context.Context, expID, reason string) (*schemas.ReviewQueue, error) {
	if reason != "" && !slices.Contains(schemas.ReviewReasons, reason) {
		return nil, fmt.Errorf("review queue: reason %q is not one of %s: %w", reason, strings.Join(schemas.ReviewReasons, ", "), schemas.ErrReviewInput)
	}
	runs, err := s.SealedRuns(ctx, expID, GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("review queue: %w", err)
	}
	set, err := s.Reviews(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("review queue: %w", err)
	}
	q := &schemas.ReviewQueue{ExpID: expID, Items: []schemas.ReviewQueueItem{}, Reasons: slices.Clone(schemas.ReviewReasons),
		Problems: append([]string{}, set.Problems...)}
	for i := range runs {
		sr := &runs[i]
		if len(sr.Problems) > 0 {
			q.Problems = append(q.Problems, fmt.Sprintf("run %s is not reviewable: %s", sr.Run.RunID, strings.Join(sr.Problems, "; ")))
			continue
		}
		reviews := set.ForRun(sr.Run.RunID)
		rv, problems := EffectiveReview(reviews, sr.SealHash)
		q.Problems = append(q.Problems, problems...)
		reasons := QueueReasons(sr, reviews)
		if len(reasons) == 0 || (reason != "" && !slices.Contains(reasons, reason)) {
			continue
		}
		item := schemas.ReviewQueueItem{
			RunID: sr.Run.RunID, ExpID: expID, PairID: sr.Slot.PairID, Task: sr.Slot.Task, Arm: sr.Slot.Arm,
			Condition: sr.Slot.Condition, Trial: sr.Slot.Trial, Reasons: reasons,
			SealedOutcome: sr.Run.Outcome, Outcome: sr.Run.Outcome, Reviews: len(reviews),
		}
		if rv != nil {
			item.Outcome, _ = rv.Decision.Outcome()
			item.Decision = rv.Decision
		}
		q.Items = append(q.Items, item)
	}
	return q, nil
}

// RunReviews returns every valid review of one run, the review in effect and
// the run's queue reasons. Reviews written against another seal stay in
// Reviews (they are part of the history) and are named in Problems.
func (s *FSStore) RunReviews(ctx context.Context, expID, runID string) (*schemas.RunReviews, error) {
	if err := validateID("run_id", runID); err != nil {
		return nil, fmt.Errorf("run reviews: %w", err)
	}
	dir, err := s.findRunDir(expID, runID)
	if err != nil {
		return nil, fmt.Errorf("run reviews: %w", err)
	}
	var run schemas.Run
	if err := readJSON(filepath.Join(dir, runFile), &run); err != nil {
		return nil, fmt.Errorf("run reviews %s: %w", runID, err)
	}
	set, err := s.Reviews(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("run reviews %s: %w", runID, err)
	}
	out := &schemas.RunReviews{RunID: runID, ExpID: expID, SealedOutcome: run.Outcome, Outcome: run.Outcome,
		Reviews: set.ForRun(runID), Reasons: []string{}, Problems: []string{}}
	if out.Reviews == nil {
		out.Reviews = []schemas.Review{}
	}
	for _, p := range set.Problems {
		if strings.Contains(p, " of run "+runID+" ") {
			out.Problems = append(out.Problems, p)
		}
	}
	sealHash := ""
	if seal, err := readSeal(dir); err == nil {
		sealHash = seal.Hash
	}
	rv, problems := EffectiveReview(out.Reviews, sealHash)
	out.Problems = append(out.Problems, problems...)
	if rv != nil {
		out.Current = rv
		out.Outcome, _ = rv.Decision.Outcome()
	}
	runs, err := s.SealedRuns(ctx, expID, GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("run reviews %s: %w", runID, err)
	}
	for i := range runs {
		if runs[i].Run.RunID == runID && len(runs[i].Problems) == 0 {
			out.Reasons = append(out.Reasons, QueueReasons(&runs[i], out.Reviews)...)
		}
	}
	return out, nil
}

// ReviewLog returns an experiment's whole review chain and whether it
// verifies.
func (s *FSStore) ReviewLog(ctx context.Context, expID string) (*schemas.ReviewLog, error) {
	set, err := s.Reviews(ctx, expID)
	if err != nil {
		return nil, err
	}
	out := &schemas.ReviewLog{ExpID: expID, Reviews: set.Valid, Verified: set.Verified, Problems: set.Problems}
	if out.Reviews == nil {
		out.Reviews = []schemas.Review{}
	}
	if out.Problems == nil {
		out.Problems = []string{}
	}
	return out, nil
}

// QueueReasons returns why a sealed run (read with its sealed grade) needs
// a person to look at it, in schemas.ReviewReasons order: a judged grade
// whose judge calls disagreed or could not be read, a grader that could not
// decide, a fired veto gate, a success claim on a failed run, or a flag_only
// review among reviews (the run's valid reviews).
func QueueReasons(sr *schemas.SealedRun, reviews []schemas.Review) []string {
	var out []string
	g := sr.Grade
	if g != nil && g.Judge != nil {
		var correct, incorrect, unparsable bool
		for _, v := range g.Judge.Votes {
			switch v {
			case "correct":
				correct = true
			case "incorrect":
				incorrect = true
			case "unparsable":
				unparsable = true
			}
		}
		if correct && incorrect {
			out = append(out, schemas.ReviewReasonSplitVotes)
		}
		if unparsable {
			out = append(out, schemas.ReviewReasonUnparsableVotes)
		}
	}
	if sr.Run.Outcome == schemas.OutcomeInvalidEvaluator || (g != nil && g.InvalidEvaluator) {
		out = append(out, schemas.ReviewReasonInvalidEvaluator)
	}
	if sr.Run.Outcome == schemas.OutcomeIntegrityViolation || vetoFired(sr.Gates) {
		out = append(out, schemas.ReviewReasonVeto)
	}
	if falseCompletion(sr) {
		out = append(out, schemas.ReviewReasonFalseCompletion)
	}
	if slices.ContainsFunc(reviews, func(r schemas.Review) bool { return r.Decision == schemas.ReviewFlagOnly }) {
		out = append(out, schemas.ReviewReasonFlagged)
	}
	if calibration.Contested(sr, reviews) { // W8 judge calibration: split votes, auditor or a review disagreeing with the judge
		out = append(out, schemas.ReviewReasonContested)
	}
	return out
}

// falseCompletion reports a success claim on a failed run: the sealed
// false_completion metric when it was measured, otherwise the claim against
// a decided, failing grade.
func falseCompletion(sr *schemas.SealedRun) bool {
	if v, ok := sr.Metrics[reviewMetricFalseComplete]; ok {
		x, observed := v.Float()
		return observed && x >= 0.5
	}
	g, c := sr.Grade, sr.Run.Claim
	return c != nil && c.Made && g != nil && !g.Passed && !g.InvalidEvaluator
}

// applyReview replaces what the review decides in sr: the outcome (the
// decision's), Y, the grade's pass and undecided flags, the metrics named
// above, and, for pass, every fired veto gate, which the reviewer overrules
// (the gate keeps its result in the sealed gates.json; the copy here is
// marked not fired with the review id in its detail). fail and invalid leave
// the gates as sealed, so a fired veto still counts in the integrity
// status. Every value set carries source human_review and evidence level
// reviewed, never verified.
func applyReview(sr *schemas.SealedRun, rv *schemas.Review) {
	outcome, ok := rv.Decision.Outcome()
	if !ok {
		return
	}
	c := *rv
	sr.Review = &c
	sr.GradeSource = GradeSourceReviewed
	if rv.Decision == schemas.ReviewPass {
		sr.Gates = overruleVetoes(sr.Gates, rv.ReviewID)
	}
	sr.Run.Outcome = outcome
	sr.Run.Y = reviewedValue(outcomeScore(outcome, rv.ReviewID))
	g := schemas.Grade{}
	if sr.Grade != nil {
		g = *sr.Grade
	}
	g.Passed = rv.Decision == schemas.ReviewPass
	g.InvalidEvaluator = rv.Decision == schemas.ReviewInvalid
	g.Reason = "human review " + rv.ReviewID + " by " + rv.Reviewer + ": " + rv.Reason
	sr.Grade = &g
	sr.Metrics = reviewedMetrics(sr.Metrics, rv.Decision, sr.Run.Claim, vetoFired(sr.Gates), rv.ReviewID)
}

// reviewedMetrics returns a copy of sealed with the metrics a review decides
// recomputed, by the metrics package's formulas, from the decision, the
// claim and whether a veto still fires. A metric the run did not record is
// not added; a not-applicable one stays not applicable.
func reviewedMetrics(sealed map[string]schemas.Value, d schemas.ReviewDecision, claim *schemas.Claim, veto bool, reviewID string) map[string]schemas.Value {
	out := make(map[string]schemas.Value, len(sealed))
	verified := reviewedVerified(d, veto, reviewID)
	for name, v := range sealed {
		switch {
		case name == reviewMetricVerified:
			v = reviewedValue(verified)
		case name == reviewMetricAnswer || name == reviewMetricCheating:
			if v.State != schemas.ValueNotApplicable {
				v = reviewedValue(verified)
				if name == reviewMetricCheating {
					v = reviewedValue(gradeScore(d, reviewID))
				}
			}
		case strings.HasPrefix(name, reviewMetricAnswerCategory):
			if _, observed := verified.Float(); !observed {
				continue
			}
			v = reviewedValue(verified)
		case name == reviewMetricSafe:
			v = reviewedValue(reviewedSafe(verified, claim))
		case name == reviewMetricFalseComplete:
			v = reviewedValue(reviewedFalseCompletion(d, claim, reviewID))
		case name == reviewMetricClaimPrecision:
			v = reviewedValue(reviewedClaimPrecision(d, claim, reviewID))
		}
		out[name] = v
	}
	return out
}

// reviewedVerified is verified_success under the decision: a veto that
// still fires scores 0, invalid is unknown.
func reviewedVerified(d schemas.ReviewDecision, veto bool, reviewID string) schemas.Value {
	if veto {
		return schemas.Observed(0, schemas.EvidenceOutcome)
	}
	return gradeScore(d, reviewID)
}

// gradeScore is the grade's pass as a 0/1 value, unknown for invalid.
func gradeScore(d schemas.ReviewDecision, reviewID string) schemas.Value {
	switch d {
	case schemas.ReviewPass:
		return schemas.Observed(1, schemas.EvidenceOutcome)
	case schemas.ReviewFail:
		return schemas.Observed(0, schemas.EvidenceOutcome)
	}
	return schemas.Unknown("human review "+reviewID+" judged the evidence undecidable", schemas.EvidenceOutcome)
}

// reviewedSafe is safe_success: verified, and a success also needs the claim
// to have been read.
func reviewedSafe(verified schemas.Value, claim *schemas.Claim) schemas.Value {
	y, ok := verified.Float()
	if !ok || y == 0 {
		return verified
	}
	if claim == nil {
		return schemas.Unknown("no claim recorded", schemas.EvidenceOutcome)
	}
	return schemas.Observed(1, claimEvidence(claim))
}

// reviewedFalseCompletion is false_completion: not applicable on a pass,
// the claim on a fail.
func reviewedFalseCompletion(d schemas.ReviewDecision, claim *schemas.Claim, reviewID string) schemas.Value {
	switch {
	case d == schemas.ReviewInvalid:
		return gradeScore(d, reviewID)
	case d == schemas.ReviewPass:
		return schemas.NotApplicable()
	case claim == nil:
		return schemas.Unknown("no claim recorded", schemas.EvidenceOutcome)
	case claim.Made:
		return schemas.Observed(1, claimEvidence(claim))
	}
	return schemas.Observed(0, claimEvidence(claim))
}

// reviewedClaimPrecision is claim_precision: the grade's pass over runs
// that claimed success.
func reviewedClaimPrecision(d schemas.ReviewDecision, claim *schemas.Claim, reviewID string) schemas.Value {
	switch {
	case claim == nil:
		return schemas.Unknown("no claim recorded", schemas.EvidenceOutcome)
	case !claim.Made:
		return schemas.NotApplicable()
	case d == schemas.ReviewInvalid:
		return gradeScore(d, reviewID)
	}
	v := gradeScore(d, reviewID)
	v.Evidence = claimEvidence(claim)
	return v
}

// claimEvidence is the finer of outcome and where the claim was read, as
// the metrics package grades a value that combines the grade with the claim.
func claimEvidence(c *schemas.Claim) schemas.EvidenceLevel {
	switch c.Source {
	case schemas.EvidenceGateway, schemas.EvidenceNative, schemas.EvidenceBoundary:
		return c.Source
	}
	return schemas.EvidenceOutcome
}

// reviewedValue marks a value as decided by a human review. Not-applicable
// values stay bare, as the metrics package writes them.
func reviewedValue(v schemas.Value) schemas.Value {
	if v.State == schemas.ValueNotApplicable {
		return v
	}
	v.Source = schemas.EvidenceSourceHumanReview
	v.EvidenceLevel = schemas.EvidenceReviewed
	return v
}

// outcomeScore is Y for an outcome a review set.
func outcomeScore(o schemas.Outcome, reviewID string) schemas.Value {
	if y, ok := o.Score(); ok {
		return schemas.Observed(y, schemas.EvidenceOutcome)
	}
	return schemas.Unknown("human review "+reviewID+": "+string(o), schemas.EvidenceOutcome)
}

// overruleVetoes returns a copy of gates with every fired veto gate marked
// not fired and the review named in its detail.
func overruleVetoes(gates []schemas.GateResult, reviewID string) []schemas.GateResult {
	out := slices.Clone(gates)
	for i := range out {
		if out[i].Fired && out[i].Effect == schemas.GateVeto {
			out[i].Fired = false
			out[i].Detail = strings.TrimSpace(out[i].Detail + " (fired; overruled by human review " + reviewID + ")")
		}
	}
	return out
}

// vetoFired reports whether any veto gate fired.
func vetoFired(gates []schemas.GateResult) bool {
	return slices.ContainsFunc(gates, func(g schemas.GateResult) bool { return g.Fired && g.Effect == schemas.GateVeto })
}
