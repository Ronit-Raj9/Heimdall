package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// reviewFixture builds e_rv: r_a (slot a1) failed a judged grade with split
// votes after claiming success, r_b (slot b1) passed its grade but a veto
// gate fired, and r_c is the uncounted compatibility run. e_open holds one
// unsealed run.
func reviewFixture(t *testing.T) (*FSStore, map[string]*schemas.SealRecord) {
	t.Helper()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_rv")
	seals := map[string]*schemas.SealRecord{}

	a := mustNewClaimRun(t, s, m, "s_a1", "r_a", schemas.OutcomeFail, &schemas.Claim{Made: true, Source: schemas.EvidenceNative})
	mustWrite(t, s, a, "grade.json", `{"passed":false,"suite_version":"1.0.0","grader_version":"1.0.0","mode":"answer","judged":true,`+
		`"judge":{"model":"judge","prompt_version":"v1","prompt_sha256":"x","votes":["correct","incorrect","incorrect"],"usd":{"state":"unknown","v":null}}}`)
	mustWrite(t, s, a, "metrics.json", `{"verified_success":{"state":"observed","v":0,"evidence":"outcome"},`+
		`"false_completion":{"state":"observed","v":1,"evidence":"native"},"safe_success":{"state":"observed","v":0,"evidence":"outcome"},`+
		`"claim_precision":{"state":"observed","v":0,"evidence":"native"},"cost_usd":{"state":"observed","v":0.25,"evidence":"gateway"}}`)
	seals[a.RunID] = mustSeal(t, s, a)

	b := mustNewRun(t, s, m, "s_b1", "r_b", 1, schemas.OutcomeIntegrityViolation)
	mustWrite(t, s, b, "grade.json", `{"passed":true,"suite_version":"1.0.0","grader_version":"1.0.0"}`)
	mustWrite(t, s, b, "gates.json", `[{"gate":"impossible_pass","version":"1.0.0","effect":"veto","fired":true,"detail":"passed an impossible task"}]`)
	mustWrite(t, s, b, "metrics.json", `{"verified_success":{"state":"observed","v":0,"evidence":"outcome"}}`)
	seals[b.RunID] = mustSeal(t, s, b)

	c := mustNewRun(t, s, m, "s_c1", "r_c", 1, schemas.OutcomePass)
	seals[c.RunID] = mustSeal(t, s, c)

	m2 := mustCreateExp(t, s, "e_open")
	mustNewRun(t, s, m2, "s_a1", "r_open", 1, schemas.OutcomeUnknown)
	return s, seals
}

// mustNewClaimRun is mustNewRun with the agent's claim recorded in run.json.
func mustNewClaimRun(t *testing.T, s *FSStore, m *schemas.Manifest, slotID, runID string, outcome schemas.Outcome, claim *schemas.Claim) *schemas.Run {
	t.Helper()
	sl := slotOf(m, slotID)
	r := &schemas.Run{RunID: runID, SlotID: slotID, PairID: sl.PairID, ExpID: m.ExpID, Attempt: 1,
		Stage: schemas.StageCleanup, ManifestHash: m.Hash, Outcome: outcome, Claim: claim}
	if _, err := s.NewRun(context.Background(), sl, r); err != nil {
		t.Fatalf("NewRun %s: %v", runID, err)
	}
	return r
}

func mustReview(t *testing.T, s *FSStore, expID, runID string, d schemas.ReviewDecision, reason string) *schemas.Review {
	t.Helper()
	rv, err := s.AddReview(context.Background(), expID, &schemas.ReviewRequest{RunID: runID, Decision: d, Reason: reason, Reviewer: "ana",
		Evidence: []string{" grade.json ", "", "judge votes"}})
	if err != nil {
		t.Fatalf("AddReview %s %s: %v", runID, d, err)
	}
	return rv
}

func TestAddReviewChain(t *testing.T) {
	ctx := context.Background()
	s, seals := reviewFixture(t)
	first := mustReview(t, s, "e_rv", "r_a", schemas.ReviewPass, "the judge misread a correct answer")
	other := mustReview(t, s, "e_rv", "r_b", schemas.ReviewFlagOnly, "veto looks right, second opinion please")
	second := mustReview(t, s, "e_rv", "r_a", schemas.ReviewFail, "on a second look the answer omits the unit")

	if first.ParentSeal != seals["r_a"].Hash || first.SealedOutcome != schemas.OutcomeFail || first.PreviousOutcome != schemas.OutcomeFail ||
		first.Supersedes != "" || first.PrevHash != "" || first.Reviewer != "ana" {
		t.Fatalf("first review = %+v", first)
	}
	if got := strings.Join(first.Evidence, "|"); got != "grade.json|judge votes" {
		t.Fatalf("evidence = %q, want trimmed non-empty items", got)
	}
	if other.PrevHash != first.Hash || second.PrevHash != other.Hash {
		t.Fatalf("chain broken: prev %q %q, hashes %q %q", other.PrevHash, second.PrevHash, first.Hash, other.Hash)
	}
	if second.Supersedes != first.ReviewID || second.PreviousOutcome != schemas.OutcomePass || second.SealedOutcome != schemas.OutcomeFail {
		t.Fatalf("second review = %+v, want it to supersede %s from pass", second, first.ReviewID)
	}
	log, err := s.ReviewLog(ctx, "e_rv")
	if err != nil {
		t.Fatalf("ReviewLog: %v", err)
	}
	if !log.Verified || len(log.Problems) != 0 || len(log.Reviews) != 3 || log.Reviews[2].ReviewID != second.ReviewID {
		t.Fatalf("log = %+v", log)
	}
	info, err := os.Stat(filepath.Join(s.Root(), "e_rv", reviewsDir, first.ReviewID, reviewFile))
	if err != nil || info.Mode().Perm() != sealedFilePerm {
		t.Fatalf("review.json mode = %v, %v; want read-only", info, err)
	}
	if err := s.Verify(ctx, "e_rv", "r_a"); err != nil {
		t.Fatalf("a review must never touch the sealed run: %v", err)
	}
	if err := s.VerifyChain("e_rv"); err != nil {
		t.Fatalf("seal chain after reviews: %v", err)
	}
	rr, err := s.RunReviews(ctx, "e_rv", "r_a")
	if err != nil {
		t.Fatalf("RunReviews: %v", err)
	}
	if len(rr.Reviews) != 2 || rr.Current == nil || rr.Current.ReviewID != second.ReviewID || rr.Outcome != schemas.OutcomeFail ||
		rr.SealedOutcome != schemas.OutcomeFail || len(rr.Problems) != 0 {
		t.Fatalf("run reviews = %+v", rr)
	}
	if got := strings.Join(rr.Reasons, ","); got != "split_votes,false_completion" {
		t.Fatalf("reasons = %q", got)
	}
}

func TestAddReviewRefuses(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewFixture(t)
	ok := schemas.ReviewRequest{RunID: "r_a", Decision: schemas.ReviewPass, Reason: "fine", Reviewer: "ana"}
	tests := []struct {
		name    string
		expID   string
		edit    func(r *schemas.ReviewRequest)
		wantErr error
	}{
		{"no reason", "e_rv", func(r *schemas.ReviewRequest) { r.Reason = "  " }, schemas.ErrReviewInput},
		{"no reviewer", "e_rv", func(r *schemas.ReviewRequest) { r.Reviewer = "" }, schemas.ErrReviewInput},
		{"reviewer on two lines", "e_rv", func(r *schemas.ReviewRequest) { r.Reviewer = "ana\nbob" }, schemas.ErrReviewInput},
		{"unknown decision", "e_rv", func(r *schemas.ReviewRequest) { r.Decision = "maybe" }, schemas.ErrReviewInput},
		{"bad run id", "e_rv", func(r *schemas.ReviewRequest) { r.RunID = "../r_a" }, schemas.ErrReviewInput},
		{"reason too long", "e_rv", func(r *schemas.ReviewRequest) { r.Reason = strings.Repeat("x", maxReviewReason+1) }, schemas.ErrReviewInput},
		{"unknown run", "e_rv", func(r *schemas.ReviewRequest) { r.RunID = "r_none" }, schemas.ErrNotFound},
		{"unsealed run", "e_open", func(r *schemas.ReviewRequest) { r.RunID = "r_open" }, schemas.ErrReviewInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := ok
			tc.edit(&req)
			if _, err := s.AddReview(ctx, tc.expID, &req); !errors.Is(err, tc.wantErr) {
				t.Fatalf("AddReview err = %v, want %v", err, tc.wantErr)
			}
		})
	}
	t.Run("tampered run", func(t *testing.T) {
		dir, err := s.findRunDir("e_rv", "r_b")
		if err != nil {
			t.Fatal(err)
		}
		tamperFile(t, filepath.Join(dir, "grade.json"), []byte(`{"passed":false}`))
		req := ok
		req.RunID = "r_b"
		if _, err := s.AddReview(ctx, "e_rv", &req); !errors.Is(err, schemas.ErrSealMismatch) {
			t.Fatalf("AddReview on a changed run folder err = %v, want seal mismatch", err)
		}
	})
	if _, err := os.Stat(filepath.Join(s.Root(), "e_rv", reviewsLogFile)); err == nil {
		set, err := s.Reviews(ctx, "e_rv")
		if err != nil || len(set.Valid) != 0 {
			t.Fatalf("refused reviews were recorded: %+v %v", set, err)
		}
	}
}

func TestReviewsTamper(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		tamper      func(t *testing.T, s *FSStore, ids []string)
		wantValid   []int // indexes into the three reviews written
		wantProblem string
	}{
		{
			name: "record edited",
			tamper: func(t *testing.T, s *FSStore, ids []string) {
				p := filepath.Join(s.Root(), "e_rv", reviewsDir, ids[0], reviewFile)
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				tamperFile(t, p, []byte(strings.Replace(string(b), `"decision": "fail"`, `"decision": "pass"`, 1)))
			},
			wantValid:   []int{1, 2},
			wantProblem: "the record was changed",
		},
		{
			name: "record rehashed",
			tamper: func(t *testing.T, s *FSStore, ids []string) {
				rv, err := s.readReviewRecord("e_rv", ids[1])
				if err != nil {
					t.Fatal(err)
				}
				rv.Decision = schemas.ReviewPass
				if rv.Hash, err = reviewHash(rv); err != nil {
					t.Fatal(err)
				}
				b, err := marshalIndent(rv)
				if err != nil {
					t.Fatal(err)
				}
				tamperFile(t, filepath.Join(s.Root(), "e_rv", reviewsDir, ids[1], reviewFile), b)
			},
			wantValid:   []int{0, 2},
			wantProblem: "reviews.log says",
		},
		{
			name: "log line malformed",
			tamper: func(t *testing.T, s *FSStore, ids []string) {
				p := filepath.Join(s.Root(), "e_rv", reviewsLogFile)
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.SplitAfter(string(b), "\n")
				lines[1] = "garbage\n"
				tamperFile(t, p, []byte(strings.Join(lines, "")))
			},
			wantValid:   []int{0},
			wantProblem: "line 2 is malformed",
		},
		{
			name: "log line removed",
			tamper: func(t *testing.T, s *FSStore, ids []string) {
				p := filepath.Join(s.Root(), "e_rv", reviewsLogFile)
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.SplitAfter(string(b), "\n")
				tamperFile(t, p, []byte(lines[0]+lines[2]))
			},
			wantValid:   []int{0},
			wantProblem: "not in reviews.log",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := reviewFixture(t)
			rvs := []*schemas.Review{
				mustReview(t, s, "e_rv", "r_a", schemas.ReviewFail, "one"),
				mustReview(t, s, "e_rv", "r_b", schemas.ReviewFail, "two"),
				mustReview(t, s, "e_rv", "r_a", schemas.ReviewInvalid, "three"),
			}
			ids := []string{rvs[0].ReviewID, rvs[1].ReviewID, rvs[2].ReviewID}
			tc.tamper(t, s, ids)
			set, err := s.Reviews(ctx, "e_rv")
			if err != nil {
				t.Fatalf("Reviews: %v", err)
			}
			if set.Verified || !strings.Contains(strings.Join(set.Problems, "\n"), tc.wantProblem) {
				t.Fatalf("problems = %q, want one containing %q", set.Problems, tc.wantProblem)
			}
			var got []string
			for _, r := range set.Valid {
				got = append(got, r.ReviewID)
			}
			var want []string
			for _, i := range tc.wantValid {
				want = append(want, ids[i])
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("valid = %v, want %v", got, want)
			}
		})
	}
}

func TestAddReviewChainsAfterTamper(t *testing.T) {
	s, _ := reviewFixture(t)
	first := mustReview(t, s, "e_rv", "r_a", schemas.ReviewFail, "one")
	p := filepath.Join(s.Root(), "e_rv", reviewsDir, first.ReviewID, reviewFile)
	tamperFile(t, p, []byte(`{"review_id":"`+first.ReviewID+`"}`))
	next := mustReview(t, s, "e_rv", "r_a", schemas.ReviewPass, "two")
	if next.PrevHash != first.Hash || next.Supersedes != "" || next.PreviousOutcome != schemas.OutcomeFail {
		t.Fatalf("next = %+v: it must chain to the logged line and ignore the changed record", next)
	}
	set, err := s.Reviews(context.Background(), "e_rv")
	if err != nil || len(set.Valid) != 1 || set.Valid[0].ReviewID != next.ReviewID {
		t.Fatalf("set = %+v, %v", set, err)
	}
	rr, err := s.RunReviews(context.Background(), "e_rv", "r_a")
	if err != nil || len(rr.Problems) != 1 || !strings.Contains(rr.Problems[0], first.ReviewID) {
		t.Fatalf("run reviews must report the ignored review: %+v, %v", rr, err)
	}
	rb, err := s.RunReviews(context.Background(), "e_rv", "r_b")
	if err != nil || len(rb.Problems) != 0 {
		t.Fatalf("another run's ignored review leaked into r_b: %+v, %v", rb, err)
	}
}

// tamperFile overwrites p after making its folder writable, as someone with
// write access to the run folder could.
func tamperFile(t *testing.T, p string, b []byte) {
	t.Helper()
	makeWritable(t, filepath.Dir(p))
	writeTampered(t, p, b)
}
