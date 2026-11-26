package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

// storedAnalyzer answers the verdict route with the stored analysis of the
// grade source the route asks for, the way the served analyzer reuses a
// saved analysis, so the test sees which source the route chose.
type storedAnalyzer struct{ st *store.FSStore }

func (s storedAnalyzer) Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	return s.st.LatestAnalysisFor(ctx, expID, gradeSource)
}

// TestOneGradingRuleEverywhere is section 5 decision 1: after a re-score,
// the comparisons list, the leaderboard, the verdict route without
// grade_source, the report card validity and the evaluation result all read
// the same analysis, the newest one of the newest re-score, even when an
// analysis of the original grading was written after it. grade_source=original
// still reads the original.
func TestOneGradingRuleEverywhere(t *testing.T) {
	ctx := context.Background()
	st, _ := regradeStore(t) // e_rg with re-scores rg_a, rg_b and rg_c; rg_c is the newest
	f := &regradeFixture{t: t, root: st.Root()}
	f.analysis("e_rg", "a_orig1", schemas.GradeSourceOriginal, "2026-11-14T02:00:00Z", "invalid")
	f.analysis("e_rg", "a_rgc", "rg_c", "2026-11-14T04:00:00Z", "score")
	f.analysis("e_rg", "a_rgb", "rg_b", "2026-11-14T05:00:00Z", "invalid")
	f.analysis("e_rg", "a_orig2", schemas.GradeSourceOriginal, "2026-11-14T06:00:00Z", "invalid")
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	x := indexAdapter{ix: ix, root: st.Root()}
	const want = "a_rgc"

	rt := router.New()
	handlers.NewExperimentsHandler(schemas.ModeRecorded, x, storedAnalyzer{st: st}, nil, nil, lib.SecurityHeaders()).
		RegisterRoutes(rt, lib.SecurityHeaders())
	get := func(path string) []byte {
		t.Helper()
		var c fasthttp.RequestCtx
		c.Request.SetRequestURI(handlers.APIPrefix + path)
		c.Request.Header.SetMethod("GET")
		rt.Handler(&c)
		if c.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, c.Response.StatusCode(), c.Response.Body())
		}
		return append([]byte(nil), c.Response.Body()...)
	}

	// The verdict route without grade_source.
	var v schemas.VerdictPayload
	if err := json.Unmarshal(get("/experiments/e_rg/verdict"), &v); err != nil {
		t.Fatal(err)
	}
	if v.AnalysisID != want || v.GradeSource != "rg_c" {
		t.Fatalf("verdict default reads %s of %s, want %s of rg_c", v.AnalysisID, v.GradeSource, want)
	}
	// The page's "Show original result" still reads the original.
	var orig schemas.VerdictPayload
	if err := json.Unmarshal(get("/experiments/e_rg/verdict?grade_source=original"), &orig); err != nil {
		t.Fatal(err)
	}
	if orig.AnalysisID != "a_orig2" || orig.GradeSource != schemas.GradeSourceOriginal {
		t.Fatalf("grade_source=original reads %s of %s, want a_orig2 of original", orig.AnalysisID, orig.GradeSource)
	}

	// The comparisons list shows the decision of that same analysis.
	var list schemas.ExperimentList
	if err := json.Unmarshal(get("/experiments"), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Experiments) != 1 || list.Experiments[0].Decision != v.Verdict.Decision {
		t.Fatalf("comparisons list %+v, want decision %q", list.Experiments, v.Verdict.Decision)
	}

	// The leaderboard keeps the comparison (the original's INVALID would
	// drop it) and reads the same decision.
	src := handlers.LeaderboardSource(x)
	if d, ok := src.LatestDecision("e_rg"); !ok || d != v.Verdict.Decision {
		t.Fatalf("leaderboard decision %q %v, want %q", d, ok, v.Verdict.Decision)
	}
	lb, err := leaderboard.Build(ctx, src, leaderboard.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lb.Entries) != 1 {
		t.Fatalf("leaderboard entries %+v, want the re-scored comparison", lb.Entries)
	}

	// The report card validity (reportcard.childInvalid reads LatestDecision
	// and LatestInvalid of this source): not invalid.
	as, ok := src.(leaderboard.AnalysisSource)
	if !ok {
		t.Fatal("the leaderboard source does not read invalid scopes")
	}
	if scope, _, invalid := as.LatestInvalid("e_rg"); invalid {
		t.Fatalf("report card validity reads the original grading: invalid %q", scope)
	}

	// The evaluation result names the same analysis (resultAnalyses).
	var ai handlers.AnalysisIndex = x
	if id, ok := ai.LatestAnalysis("e_rg"); !ok || id != want {
		t.Fatalf("evaluation result analysis %q %v, want %s", id, ok, want)
	}
}
