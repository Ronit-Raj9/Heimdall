package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
)

func runByID(t *testing.T, runs []schemas.SealedRun, id string) schemas.SealedRun {
	t.Helper()
	for _, r := range runs {
		if r.Run.RunID == id {
			return r
		}
	}
	t.Fatalf("run %s not loaded", id)
	return schemas.SealedRun{}
}

func TestReviewedRuns(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	before, err := s.SealedRuns(ctx, "e_rv", GradeSourceOriginal)
	if err != nil {
		t.Fatalf("SealedRuns: %v", err)
	}
	t.Run("no reviews reads as sealed", func(t *testing.T) {
		got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
		if err != nil {
			t.Fatalf("SealedRuns reviewed: %v", err)
		}
		if !reflect.DeepEqual(got, before) {
			t.Fatalf("reviewed with no reviews differs from original")
		}
		if n, c := ReviewCounts(got); n != 0 || c != 0 {
			t.Fatalf("counts = %d %d", n, c)
		}
	})

	pass := mustReview(t, s, "e_rv", "r_a", schemas.ReviewPass, "judge misread the answer")
	mustReview(t, s, "e_rv", "r_b", schemas.ReviewFlagOnly, "look at the veto")
	t.Run("original unchanged", func(t *testing.T) {
		got, err := s.SealedRuns(ctx, "e_rv", GradeSourceOriginal)
		if err != nil {
			t.Fatalf("SealedRuns: %v", err)
		}
		if !reflect.DeepEqual(got, before) {
			t.Fatalf("original grade source moved after reviews were written")
		}
	})
	t.Run("pass applied", func(t *testing.T) {
		got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
		if err != nil {
			t.Fatalf("SealedRuns reviewed: %v", err)
		}
		a := runByID(t, got, "r_a")
		if a.Run.Outcome != schemas.OutcomePass || a.GradeSource != GradeSourceReviewed || a.Review == nil || a.Review.ReviewID != pass.ReviewID {
			t.Fatalf("r_a = outcome %s source %s review %+v", a.Run.Outcome, a.GradeSource, a.Review)
		}
		if y, ok := a.Run.Y.Float(); !ok || y != 1 || a.Run.Y.EvidenceLevel != schemas.EvidenceReviewed || a.Run.Y.Source != schemas.EvidenceSourceHumanReview {
			t.Fatalf("r_a Y = %+v", a.Run.Y)
		}
		vs := a.Metrics["verified_success"]
		if y, ok := vs.Float(); !ok || y != 1 || vs.EvidenceLevel != schemas.EvidenceReviewed {
			t.Fatalf("verified_success = %+v", vs)
		}
		if a.Metrics["false_completion"].State != schemas.ValueNotApplicable {
			t.Fatalf("false_completion = %+v, want not applicable on a pass", a.Metrics["false_completion"])
		}
		if c, _ := a.Metrics["cost_usd"].Float(); c != 0.25 || a.Metrics["cost_usd"].EvidenceLevel != "" {
			t.Fatalf("cost_usd = %+v, want it as sealed", a.Metrics["cost_usd"])
		}
		if a.Grade == nil || !a.Grade.Passed || !strings.Contains(a.Grade.Reason, pass.ReviewID) || !a.Grade.Judged {
			t.Fatalf("grade = %+v", a.Grade)
		}
		b := runByID(t, got, "r_b")
		if b.Run.Outcome != schemas.OutcomeIntegrityViolation || b.Review != nil || b.GradeSource != GradeSourceOriginal {
			t.Fatalf("a flag_only review must not change r_b: %+v", b.Run)
		}
		if n, c := ReviewCounts(got); n != 1 || c != 1 {
			t.Fatalf("counts = %d %d, want 1 1", n, c)
		}
	})

	fail := mustReview(t, s, "e_rv", "r_a", schemas.ReviewFail, "second look: wrong unit")
	mustReview(t, s, "e_rv", "r_a", schemas.ReviewFlagOnly, "still unsure")
	t.Run("newest decision supersedes", func(t *testing.T) {
		got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
		if err != nil {
			t.Fatalf("SealedRuns reviewed: %v", err)
		}
		a := runByID(t, got, "r_a")
		if a.Run.Outcome != schemas.OutcomeFail || a.Review.ReviewID != fail.ReviewID {
			t.Fatalf("r_a = %s by %s, want fail by %s", a.Run.Outcome, a.Review.ReviewID, fail.ReviewID)
		}
		if fc, _ := a.Metrics["false_completion"].Float(); fc != 1 {
			t.Fatalf("false_completion = %+v, want 1: claimed success on a failed run", a.Metrics["false_completion"])
		}
		if n, c := ReviewCounts(got); n != 1 || c != 0 {
			t.Fatalf("counts = %d %d, want 1 reviewed, 0 changed", n, c)
		}
	})
}

func TestReviewedRunsVetoOverrule(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	rv := mustReview(t, s, "e_rv", "r_b", schemas.ReviewPass, "the impossible_pass rule misfired: the task was possible")
	got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
	if err != nil {
		t.Fatalf("SealedRuns reviewed: %v", err)
	}
	b := runByID(t, got, "r_b")
	if b.Run.Outcome != schemas.OutcomePass || vetoFired(b.Gates) || !strings.Contains(b.Gates[0].Detail, rv.ReviewID) {
		t.Fatalf("r_b = %s gates %+v", b.Run.Outcome, b.Gates)
	}
	if y, _ := b.Metrics["verified_success"].Float(); y != 1 {
		t.Fatalf("verified_success = %+v", b.Metrics["verified_success"])
	}
	orig, err := s.SealedRuns(ctx, "e_rv", GradeSourceOriginal)
	if err != nil {
		t.Fatal(err)
	}
	if !vetoFired(runByID(t, orig, "r_b").Gates) {
		t.Fatalf("the sealed gates must still show the veto")
	}

	t.Run("fail keeps the veto", func(t *testing.T) {
		mustReview(t, s, "e_rv", "r_b", schemas.ReviewFail, "the veto was right after all")
		got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
		if err != nil {
			t.Fatal(err)
		}
		b := runByID(t, got, "r_b")
		if b.Run.Outcome != schemas.OutcomeFail || !vetoFired(b.Gates) {
			t.Fatalf("r_b = %s gates %+v", b.Run.Outcome, b.Gates)
		}
	})
}

func TestReviewedRunsIgnoresOtherSeal(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	log, unlock, err := s.openReviewsLog("e_rv")
	if err != nil {
		t.Fatal(err)
	}
	rv := &schemas.Review{ReviewID: "rv_forged", ExpID: "e_rv", RunID: "r_a", ParentSeal: strings.Repeat("0", 64),
		Decision: schemas.ReviewPass, Reason: "written against another seal", Reviewer: "eve", SealedOutcome: schemas.OutcomeFail}
	if rv.Hash, err = reviewHash(rv); err != nil {
		t.Fatal(err)
	}
	if err := s.writeReviewRecord("e_rv", rv); err != nil {
		t.Fatal(err)
	}
	if err := appendLineTo(log, []byte(rv.ReviewID+" "+rv.RunID+" "+rv.Hash)); err != nil {
		t.Fatal(err)
	}
	unlock()
	got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
	if err != nil {
		t.Fatal(err)
	}
	if a := runByID(t, got, "r_a"); a.Run.Outcome != schemas.OutcomeFail || a.Review != nil {
		t.Fatalf("a review against another seal was applied: %+v", a.Run)
	}
	rr, err := s.RunReviews(ctx, "e_rv", "r_a")
	if err != nil {
		t.Fatal(err)
	}
	if rr.Current != nil || len(rr.Reviews) != 1 || !strings.Contains(strings.Join(rr.Problems, ""), "parent_seal") {
		t.Fatalf("run reviews = %+v", rr)
	}
}

func TestReviewQueue(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	mustReview(t, s, "e_rv", "r_a", schemas.ReviewFlagOnly, "look again")
	mustReview(t, s, "e_rv", "r_b", schemas.ReviewPass, "veto misfired")
	tests := []struct {
		reason  string
		want    []string // run id: reasons
		wantErr error
	}{
		{reason: "", want: []string{"r_a:split_votes,false_completion,flagged,contested", "r_b:veto"}}, // W8 judge calibration: split votes are contested
		{reason: "veto", want: []string{"r_b:veto"}},
		{reason: "flagged", want: []string{"r_a:split_votes,false_completion,flagged,contested"}},
		{reason: "contested", want: []string{"r_a:split_votes,false_completion,flagged,contested"}}, // W8 judge calibration
		{reason: "invalid_evaluator", want: nil},
		{reason: "nonsense", wantErr: schemas.ErrReviewInput},
	}
	for _, tc := range tests {
		t.Run("reason "+tc.reason, func(t *testing.T) {
			q, err := s.ReviewQueue(ctx, "e_rv", tc.reason)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if q.Items == nil || q.Problems == nil || len(q.Reasons) != len(schemas.ReviewReasons) {
				t.Fatalf("queue lists must be arrays, never null: %+v", q)
			}
			var got []string
			for _, it := range q.Items {
				got = append(got, it.RunID+":"+strings.Join(it.Reasons, ","))
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("queue = %v, want %v", got, tc.want)
			}
			for _, it := range q.Items {
				if it.RunID == "r_b" && (it.Outcome != schemas.OutcomePass || it.SealedOutcome != schemas.OutcomeIntegrityViolation ||
					it.Decision != schemas.ReviewPass || it.Reviews != 1 || it.Task != "t01" || it.Arm != "b") {
					t.Fatalf("r_b item = %+v", it)
				}
			}
		})
	}
}

func TestQueueReasons(t *testing.T) {
	judged := func(votes ...string) *schemas.Grade {
		return &schemas.Grade{Judged: true, Judge: &schemas.JudgeRecord{Votes: votes}}
	}
	tests := []struct {
		name string
		sr   schemas.SealedRun
		want string
	}{
		{"unanimous judge", schemas.SealedRun{Grade: judged("correct", "correct", "correct")}, ""},
		// W8 judge calibration: split votes and an auditor disagreement are contested.
		{"split judge", schemas.SealedRun{Grade: judged("correct", "incorrect", "correct")}, "split_votes,contested"},
		{"auditor disagrees", schemas.SealedRun{Grade: &schemas.Grade{Judged: true,
			Judge: &schemas.JudgeRecord{Votes: []string{"correct"}, Audit: &schemas.JudgeAudit{Verdict: "incorrect"}}}}, "contested"},
		{"unparsable vote", schemas.SealedRun{Grade: judged("incorrect", "unparsable", "incorrect")}, "unparsable_votes"},
		{"invalid evaluator", schemas.SealedRun{Run: schemas.Run{Outcome: schemas.OutcomeInvalidEvaluator}}, "invalid_evaluator"},
		{"undecided grade", schemas.SealedRun{Grade: &schemas.Grade{InvalidEvaluator: true}}, "invalid_evaluator"},
		{"veto outcome", schemas.SealedRun{Run: schemas.Run{Outcome: schemas.OutcomeIntegrityViolation}}, "veto"},
		{"claim on failed grade, no metric", schemas.SealedRun{Run: schemas.Run{Claim: &schemas.Claim{Made: true}}, Grade: &schemas.Grade{}}, "false_completion"},
		{"metric says no false completion", schemas.SealedRun{Run: schemas.Run{Claim: &schemas.Claim{Made: true}}, Grade: &schemas.Grade{},
			Metrics: map[string]schemas.Value{"false_completion": schemas.Observed(0, schemas.EvidenceOutcome)}}, ""},
		{"plain pass", schemas.SealedRun{Run: schemas.Run{Outcome: schemas.OutcomePass}, Grade: &schemas.Grade{Passed: true}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(QueueReasons(&tc.sr, nil), ","); got != tc.want {
				t.Fatalf("reasons = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReviewedMetricsMatchMetricsPackage checks the formulas applyReview
// uses against the metrics package itself, over every decision, claim and
// veto state, so the two can never drift apart unnoticed.
func TestReviewedMetricsMatchMetricsPackage(t *testing.T) {
	ctx := context.Background()
	names := []string{reviewMetricVerified, reviewMetricSafe, reviewMetricAnswer, reviewMetricFalseComplete, reviewMetricClaimPrecision, reviewMetricCheating}
	byName := map[string]schemas.Metric{}
	for _, m := range metrics.Builtin() {
		byName[m.GetName()] = m
	}
	sealed := map[string]schemas.Value{}
	for _, n := range names {
		if byName[n] == nil {
			t.Fatalf("metric %s is not registered", n)
		}
		sealed[n] = schemas.Observed(0, schemas.EvidenceOutcome)
	}
	claims := map[string]*schemas.Claim{"none": nil, "made": {Made: true, Source: schemas.EvidenceNative}, "not made": {Made: false, Source: schemas.EvidenceOutcome}}
	rc := &schemas.RunContext{Task: &schemas.Task{Impossible: true}}
	for _, d := range []schemas.ReviewDecision{schemas.ReviewPass, schemas.ReviewFail, schemas.ReviewInvalid} {
		for cname, claim := range claims {
			for _, veto := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/veto=%v", d, cname, veto), func(t *testing.T) {
					var gates []schemas.GateResult
					if veto {
						gates = []schemas.GateResult{{Gate: "g", Effect: schemas.GateVeto, Fired: true}}
					}
					g := &schemas.Grade{Passed: d == schemas.ReviewPass, InvalidEvaluator: d == schemas.ReviewInvalid, Mode: "answer"}
					got := reviewedMetrics(sealed, d, claim, veto, "rv_x")
					ev := &schemas.Evidence{Grade: g, Gates: gates, Claim: claim}
					for _, n := range names {
						want, err := byName[n].Compute(ctx, rc, ev)
						if err != nil {
							t.Fatalf("%s: %v", n, err)
						}
						if !sameNumber(got[n], want) {
							gb, _ := json.Marshal(got[n])
							wb, _ := json.Marshal(want)
							t.Errorf("%s = %s, metrics package gives %s", n, gb, wb)
						}
					}
				})
			}
		}
	}
}

// sameNumber compares state, number and legacy evidence level; reasons and
// the review markers differ on purpose.
func sameNumber(a, b schemas.Value) bool {
	if a.State != b.State || a.Evidence != b.Evidence {
		return false
	}
	x, okA := a.Float()
	y, okB := b.Float()
	return okA == okB && x == y
}

func TestReviewedRunsSkipsTamperedRun(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	mustReview(t, s, "e_rv", "r_a", schemas.ReviewPass, "fine")
	dir, err := s.findRunDir("e_rv", "r_a")
	if err != nil {
		t.Fatal(err)
	}
	tamperFile(t, filepath.Join(dir, "metrics.json"), []byte(`{}`))
	got, err := s.SealedRuns(ctx, "e_rv", GradeSourceReviewed)
	if err != nil {
		t.Fatal(err)
	}
	a := runByID(t, got, "r_a")
	if a.Review != nil || len(a.Problems) == 0 {
		t.Fatalf("a review was applied to a run whose seal does not verify: %+v", a)
	}
	q, err := s.ReviewQueue(ctx, "e_rv", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(q.Problems, "\n"), "run r_a is not reviewable") {
		t.Fatalf("queue problems = %q", q.Problems)
	}
	if _, err := os.Stat(filepath.Join(dir, "seal.json")); err != nil {
		t.Fatal(err)
	}
}
