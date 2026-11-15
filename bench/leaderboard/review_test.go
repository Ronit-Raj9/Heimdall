package leaderboard

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// reviewSource has config h_a in e_one, where a reviewer passed the failed
// t1 run, and e_two with no review. e_one also has a regrade, which grade
// source reviewed must not read.
func reviewSource(t *testing.T) *fakeSource {
	f := newFake()
	f.exp("e_one", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command")})
	failed := f.fail("e_one", "t1", "A", "clean", 1, 0.1)
	passed := f.pass("e_one", "t2", "A", "clean", 1, 0.1)
	f.exp("e_two", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("a two", "h_a", "command")})
	other := f.fail("e_two", "t1", "A", "clean", 1, 0.1)
	rv := &schemas.Review{ReviewID: "rv_1", RunID: failed, Decision: schemas.ReviewPass, SealedOutcome: schemas.OutcomeFail}
	f.regraded["e_one/"+GradeSourceReviewed] = []schemas.SealedRun{
		{Run: schemas.Run{RunID: failed, Outcome: schemas.OutcomePass, Y: schemas.Observed(1, schemas.EvidenceOutcome)},
			GradeSource: GradeSourceReviewed, Review: rv, Metrics: metricsOf(t, metricsJSON(1, 0.1))},
		{Run: schemas.Run{RunID: passed, Outcome: schemas.OutcomePass}, GradeSource: GradeSourceOriginal, Metrics: metricsOf(t, metricsJSON(1, 0.1))},
	}
	f.regraded["e_two/"+GradeSourceReviewed] = []schemas.SealedRun{
		{Run: schemas.Run{RunID: other, Outcome: schemas.OutcomeFail}, GradeSource: GradeSourceOriginal, Metrics: metricsOf(t, metricsJSON(0, 0.1))},
	}
	f.regrades["e_one"] = []Regrade{{ID: "rg_x", CreatedAt: t0}}
	f.regraded["e_one/rg_x"] = []schemas.SealedRun{
		{Run: schemas.Run{RunID: passed, Outcome: schemas.OutcomeFail}, GradeSource: "rg_x", Metrics: metricsOf(t, metricsJSON(0, 0.1))},
	}
	return f
}

func TestBuildGradeSourceReviewed(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		source     string
		passes     int
		reviewed   int
		changed    int
		sources    string
		mdContains string
	}{
		{source: GradeSourceOriginal, passes: 1, sources: "e_two:original:0,e_one:original:0", mdContains: "Grade source original"},
		{source: GradeSourceLatest, passes: 0, sources: "e_two:original:0,e_one:rg_x:0", mdContains: "Grade source latest"},
		{source: GradeSourceReviewed, passes: 2, reviewed: 1, changed: 1, sources: "e_two:original:0,e_one:reviewed:1", mdContains: "1 run on this board took the outcome from a human review"},
	}
	for _, tc := range tests {
		t.Run(tc.source, func(t *testing.T) {
			lb, err := Build(ctx, reviewSource(t), Query{GradeSource: tc.source, Now: t0})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(lb.Entries) != 1 || lb.Entries[0].Passes.Num != tc.passes {
				t.Fatalf("entries = %+v, want one with %d passes", lb.Entries, tc.passes)
			}
			if lb.ReviewedRuns != tc.reviewed || lb.ReviewChangedRuns != tc.changed {
				t.Fatalf("reviewed %d changed %d, want %d %d", lb.ReviewedRuns, lb.ReviewChangedRuns, tc.reviewed, tc.changed)
			}
			var got []string
			for _, gs := range lb.GradeSources {
				got = append(got, gs.Experiment+":"+gs.Source+":"+strconv.Itoa(gs.ReviewedRuns))
			}
			if strings.Join(got, ",") != tc.sources {
				t.Fatalf("grade sources = %v, want %s", got, tc.sources)
			}
			md := Markdown(lb)
			if !strings.Contains(md, tc.mdContains) {
				t.Fatalf("markdown lacks %q:\n%s", tc.mdContains, md)
			}
		})
	}
}

func TestGradeSourceReviewedIsAccepted(t *testing.T) {
	if _, err := (Query{GradeSource: GradeSourceReviewed}).Normalize(); err != nil {
		t.Fatalf("Normalize reviewed: %v", err)
	}
	if _, err := (Query{GradeSource: "rv_1"}).Normalize(); err == nil {
		t.Fatal("a review id must not be a grade source")
	}
}
