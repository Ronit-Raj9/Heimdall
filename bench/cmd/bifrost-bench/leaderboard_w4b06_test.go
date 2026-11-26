package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// regradeFixture writes one experiment's analyses and regrades under a run
// root: each analysis as analysis/<id>/verdict.json, each regrade as
// regrades/<id>/regrade.json.
type regradeFixture struct {
	t    *testing.T
	root string
}

func newRegradeFixture(t *testing.T) *regradeFixture {
	return &regradeFixture{t: t, root: t.TempDir()}
}

func (f *regradeFixture) file(path, body string) {
	f.t.Helper()
	p := filepath.Join(f.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *regradeFixture) analysis(exp, id, source, at, decision string) {
	f.file(exp+"/analysis/"+id+"/verdict.json",
		`{"analysis_id":"`+id+`","grade_source":"`+source+`","created_at":"`+at+`","verdict":{"decision":"`+decision+`"}}`)
}

func (f *regradeFixture) regrade(exp, id, at string) {
	f.file(exp+"/regrades/"+id+"/regrade.json",
		`{"regrade_id":"`+id+`","exp_id":"`+exp+`","suite_version":"1.0.0","grader_version":"g2","created_at":"`+at+`"}`)
}

// TestLatestDecisionFollowsNewestRegrade checks the list reads the newest
// re-score even when an analysis of the original was written after it.
func TestLatestDecisionFollowsNewestRegrade(t *testing.T) {
	f := newRegradeFixture(t)
	f.analysis("e_rg", "a_orig1", schemas.GradeSourceOriginal, "2026-11-10T00:00:00Z", "invalid")
	f.regrade("e_rg", "rg_1", "2026-11-11T00:00:00Z")
	f.analysis("e_rg", "a_rg1", "rg_1", "2026-11-11T01:00:00Z", "inconclusive")
	f.analysis("e_rg", "a_orig2", schemas.GradeSourceOriginal, "2026-11-12T00:00:00Z", "invalid")
	x := indexAdapter{root: f.root}
	if got, ok := x.LatestDecision("e_rg"); !ok || got != schemas.DecisionInconclusive {
		t.Fatalf("LatestDecision = %q %v, want inconclusive", got, ok)
	}
	if _, _, ok := x.LatestInvalid("e_rg"); ok {
		t.Fatal("LatestInvalid reports the original grading after a re-score")
	}
}

// TestLatestAnalysisFollowsNewestRegrade checks the one rule: the newest
// analysis of the newest re-score; with no analysis of it, or no re-score,
// the newest analysis of the original grading.
func TestLatestAnalysisFollowsNewestRegrade(t *testing.T) {
	f := newRegradeFixture(t)
	// Two re-scores; the newest has an analysis, an older analysis of the
	// older re-score and a newer one of the original must lose.
	f.analysis("e_two", "a_orig", schemas.GradeSourceOriginal, "2026-11-10T00:00:00Z", "invalid")
	f.regrade("e_two", "rg_old", "2026-11-11T00:00:00Z")
	f.regrade("e_two", "rg_new", "2026-11-12T00:00:00Z")
	f.analysis("e_two", "a_new", "rg_new", "2026-11-12T01:00:00Z", "improved")
	f.analysis("e_two", "a_old", "rg_old", "2026-11-13T00:00:00Z", "regressed")
	f.analysis("e_two", "a_orig2", schemas.GradeSourceOriginal, "2026-11-14T00:00:00Z", "invalid")
	// The newest re-score has no analysis yet: the original answers, not an
	// older re-score.
	f.analysis("e_pending", "a_orig", schemas.GradeSourceOriginal, "2026-11-10T00:00:00Z", "invalid")
	f.regrade("e_pending", "rg_old", "2026-11-11T00:00:00Z")
	f.analysis("e_pending", "a_old", "rg_old", "2026-11-11T01:00:00Z", "improved")
	f.regrade("e_pending", "rg_new", "2026-11-12T00:00:00Z")
	// No re-score: the newest analysis of the original.
	f.analysis("e_plain", "a_1", schemas.GradeSourceOriginal, "2026-11-10T00:00:00Z", "invalid")
	f.analysis("e_plain", "a_2", schemas.GradeSourceOriginal, "2026-11-11T00:00:00Z", "improved")
	x := indexAdapter{root: f.root}
	for exp, want := range map[string]string{"e_two": "a_new", "e_pending": "a_orig", "e_plain": "a_2"} {
		if got, ok := x.LatestAnalysis(exp); !ok || got != want {
			t.Errorf("%s: LatestAnalysis = %q %v, want %q", exp, got, ok, want)
		}
	}
	if _, ok := x.LatestAnalysis("e_none"); ok {
		t.Error("an experiment without analyses has a latest analysis")
	}
}
